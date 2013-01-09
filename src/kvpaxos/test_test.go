package kvpaxos

import "testing"
import "runtime"
import "strconv"
import "os"
import "time"
import "fmt"
import "math/rand"

func check(t *testing.T, ck *Clerk, key string, value string) {
  v := ck.Get(key)
  if v != value {
    t.Fatalf("Get(%v) -> %v, expected %v", key, v, value)
  }
}

func port(tag string, host int) string {
  s := "/var/tmp/kv-"
  s += strconv.Itoa(os.Getuid()) + "-"
  s += strconv.Itoa(os.Getpid()) + "-"
  s += tag + "-"
  s += strconv.Itoa(host)
  return s
}

func cleanup(kva []*KVPaxos) {
  for i := 0; i < len(kva); i++ {
    if kva[i] != nil {
      kva[i].kill()
    }
  }
}

func TestBasic(t *testing.T) {
  runtime.GOMAXPROCS(4)

  const nservers = 3
  var kva []*KVPaxos = make([]*KVPaxos, nservers)
  var kvh []string = make([]string, nservers)
  defer cleanup(kva)

  for i := 0; i < nservers; i++ {
    kvh[i] = port("basic", i)
  }
  for i := 0; i < nservers; i++ {
    kva[i] = StartServer(kvh, i)
  }

  ck := MakeClerk(kvh)
  var cka [nservers]*Clerk
  for i := 0; i < nservers; i++ {
    cka[i] = MakeClerk([]string{kvh[i]})
  }

  fmt.Printf("Basic put/get: ")

  ck.Put("a", "aa")
  check(t, ck, "a", "aa")

  cka[1].Put("a", "aaa")

  check(t, cka[2], "a", "aaa")
  check(t, cka[1], "a", "aaa")
  check(t, ck, "a", "aaa")

  fmt.Printf("OK\n")

  fmt.Printf("Concurrent clients: ")

  for iters := 0; iters < 20; iters++ {
    const npara = 30
    var ca [npara]chan bool
    for nth := 0; nth < npara; nth++ {
      ca[nth] = make(chan bool)
      go func(me int) {
        defer func() { ca[me] <- true }()
        ci := (rand.Int() % nservers)
        myck := MakeClerk([]string{kvh[ci]})
        if (rand.Int() % 1000) < 500 {
          myck.Put("b", strconv.Itoa(rand.Int()))
        } else {
          myck.Get("b")
        }
      }(nth)
    }
    for nth := 0; nth < npara; nth++ {
      <- ca[nth]
    }
    var va [nservers]string
    for i := 0; i < nservers; i++ {
      va[i] = cka[i].Get("b")
      if va[i] != va[0] {
        t.Fatalf("mismatch")
      }
    }
  }

  fmt.Printf("OK\n")

  time.Sleep(1 * time.Second)
}

func pp(tag string, src int, dst int) string {
  s := "/var/tmp/kv-" + tag + "-"
  s += strconv.Itoa(os.Getuid()) + "-"
  s += strconv.Itoa(os.Getpid()) + "-"
  s += strconv.Itoa(src) + "-"
  s += strconv.Itoa(dst)
  return s
}

func part(t *testing.T, tag string, npaxos int, p1 []int, p2 []int, p3 []int) {
  for i := 0; i < npaxos; i++ {
    for j := 0; j < npaxos; j++ {
      ij := pp(tag, i, j)
      os.Remove(ij)
    }
  }

  pa := [][]int{p1, p2, p3}
  for pi := 0; pi < len(pa); pi++ {
    p := pa[pi]
    for i := 0; i < len(p); i++ {
      for j := 0; j < len(p); j++ {
        ij := pp(tag, p[i], p[j])
        pj := port(tag, p[j])
        err := os.Link(pj, ij)
        if err != nil {
          t.Fatalf("os.Link(%v, %v): %v\n", pj, ij, err)
        }
      }
    }
  }
}

func TestPartition(t *testing.T) {
  runtime.GOMAXPROCS(4)

  tag := "partition"
  const nservers = 5
  var kva []*KVPaxos = make([]*KVPaxos, nservers)
  defer cleanup(kva)

  for i := 0; i < nservers; i++ {
    var kvh []string = make([]string, nservers)
    for j := 0; j < nservers; j++ {
      if j == i {
        kvh[j] = port(tag, i)
      } else {
        kvh[j] = pp(tag, i, j)
      }
    }
    kva[i] = StartServer(kvh, i)
  }
  defer part(t, tag, nservers, []int{}, []int{}, []int{})

  var cka [nservers]*Clerk
  for i := 0; i < nservers; i++ {
    cka[i] = MakeClerk([]string{port(tag, i)})
  }

  fmt.Printf("No partition: ")

  part(t, tag, nservers, []int{0,1,2,3,4}, []int{}, []int{})
  cka[0].Put("1", "12")
  cka[2].Put("1", "13")
  check(t, cka[3], "1", "13")

  fmt.Printf("OK\n")

  fmt.Printf("Progress in majority: ")

  part(t, tag, nservers, []int{2,3,4}, []int{0,1}, []int{})
  cka[2].Put("1", "14")
  check(t, cka[4], "1", "14")

  fmt.Printf("OK\n")

  fmt.Printf("No progress in minority: ")

  done0 := false
  done1 := false
  go func() {
    cka[0].Put("1", "15")
    done0 = true
  }()
  go func() {
    cka[1].Get("1")
    done1 = true
  }()
  time.Sleep(time.Second)
  if done0 {
    t.Fatalf("Put in minority completed")
  }
  if done1 {
    t.Fatalf("Get in minority completed")
  }
  check(t, cka[4], "1", "14")
  cka[3].Put("1", "16")
  check(t, cka[4], "1", "16")

  fmt.Printf("OK\n")

  fmt.Printf("Completion after heal: ")

  part(t, tag, nservers, []int{0,2,3,4}, []int{1}, []int{})
  for iters := 0; iters < 30; iters++ {
    if done0 {
      break
    }
    time.Sleep(100 * time.Millisecond)
  }
  if done0 == false {
    t.Fatalf("Put did not complete")
  }
  if done1 {
    t.Fatalf("Get in minority completed")
  }
  check(t, cka[4], "1", "15")
  check(t, cka[0], "1", "15")

  part(t, tag, nservers, []int{0,1,2}, []int{3,4}, []int{})
  for iters := 0; iters < 100; iters++ {
    if done1 {
      break
    }
    time.Sleep(100 * time.Millisecond)
  }
  if done1 == false {
    t.Fatalf("Get did not complete")
  }
  check(t, cka[1], "1", "15")

  fmt.Printf("OK\n")
}

func TestUnreliable(t *testing.T) {
  runtime.GOMAXPROCS(4)

  const nservers = 3
  var kva []*KVPaxos = make([]*KVPaxos, nservers)
  var kvh []string = make([]string, nservers)
  defer cleanup(kva)

  for i := 0; i < nservers; i++ {
    kvh[i] = port("un", i)
  }
  for i := 0; i < nservers; i++ {
    kva[i] = StartServer(kvh, i)
    kva[i].unreliable = true
  }

  ck := MakeClerk(kvh)
  var cka [nservers]*Clerk
  for i := 0; i < nservers; i++ {
    cka[i] = MakeClerk([]string{kvh[i]})
  }

  fmt.Printf("Basic put/get, unreliable: ")

  ck.Put("a", "aa")
  check(t, ck, "a", "aa")

  cka[1].Put("a", "aaa")

  check(t, cka[2], "a", "aaa")
  check(t, cka[1], "a", "aaa")
  check(t, ck, "a", "aaa")

  fmt.Printf("OK\n")

  fmt.Printf("Sequence of puts, unreliable: ")

  const ncli = 30
  var ca [ncli]chan bool
  for cli := 0; cli < ncli; cli++ {
    ca[cli] = make(chan bool)
    go func(me int) {
      ok := false
      defer func() { ca[me] <- ok }()
      sa := make([]string, len(kvh))
      copy(sa, kvh)
      for i := range sa {
        j := rand.Intn(i+1)
        sa[i], sa[j] = sa[j], sa[i]
      }
      myck := MakeClerk(sa)
      key := strconv.Itoa(me)
      myck.Put(key, "0")
      myck.Put(key, "1")
      myck.Put(key, "2")
      time.Sleep(100 * time.Millisecond)
      if myck.Get(key) != "2" {
        t.Fatalf("wrong value")
      }
      if myck.Get(key) != "2" {
        t.Fatalf("wrong value")
      }
      ok = true
    }(cli)
  }
  for cli := 0; cli < ncli; cli++ {
    x := <- ca[cli]
    if x == false {
      t.Fatalf("failure")
    }
  }

  fmt.Printf("OK\n")

  fmt.Printf("Concurrent clients, unreliable: ")

  for iters := 0; iters < 20; iters++ {
    const ncli = 30
    var ca [ncli]chan bool
    for cli := 0; cli < ncli; cli++ {
      ca[cli] = make(chan bool)
      go func(me int) {
        defer func() { ca[me] <- true }()
        sa := make([]string, len(kvh))
        copy(sa, kvh)
        for i := range sa {
          j := rand.Intn(i+1)
          sa[i], sa[j] = sa[j], sa[i]
        }
        myck := MakeClerk(sa)
        if (rand.Int() % 1000) < 500 {
          myck.Put("b", strconv.Itoa(rand.Int()))
        } else {
          myck.Get("b")
        }
      }(cli)
    }
    for cli := 0; cli < ncli; cli++ {
      <- ca[cli]
    }

    var va [nservers]string
    for i := 0; i < nservers; i++ {
      va[i] = cka[i].Get("b")
      if va[i] != va[0] {
        t.Fatalf("mismatch; 0 got %v, %v got %v", va[0], i, va[i])
      }
    }
  }

  fmt.Printf("OK\n")

  time.Sleep(1 * time.Second)
}

func TestHole(t *testing.T) {
  runtime.GOMAXPROCS(4)

  fmt.Printf("Tolerates holes in paxos sequence: ")

  tag := "hole"
  const nservers = 5
  var kva []*KVPaxos = make([]*KVPaxos, nservers)
  defer cleanup(kva)

  for i := 0; i < nservers; i++ {
    var kvh []string = make([]string, nservers)
    for j := 0; j < nservers; j++ {
      if j == i {
        kvh[j] = port(tag, i)
      } else {
        kvh[j] = pp(tag, i, j)
      }
    }
    kva[i] = StartServer(kvh, i)
  }
  defer part(t, tag, nservers, []int{}, []int{}, []int{})

  for iters := 0; iters < 5; iters++ {
    part(t, tag, nservers, []int{0,1,2,3,4}, []int{}, []int{})

    ck2 := MakeClerk([]string{port(tag, 2)})
    ck2.Put("q", "q")

    done := false
    const nclients = 10
    var ca [nclients]chan bool
    for xcli := 0; xcli < nclients; xcli++ {
      ca[xcli] = make(chan bool)
      go func(cli int) {
        ok := false
        defer func() { ca[cli] <- ok }()
        var cka [nservers]*Clerk
        for i := 0; i < nservers; i++ {
          cka[i] = MakeClerk([]string{port(tag, i)})
        }
        key := strconv.Itoa(cli)
        last := ""
        cka[0].Put(key, last)
        for done == false {
          ci := (rand.Int() % 2)
          if (rand.Int() % 1000) < 500 {
            nv := strconv.Itoa(rand.Int())
            cka[ci].Put(key, nv)
            last = nv
          } else {
            v := cka[ci].Get(key)
            if v != last {
              t.Fatalf("%v: wrong value, key %v, wanted %v, got %v",
                cli, key, last, v)
            }
          }
        }
        ok = true
      } (xcli)
    }

    time.Sleep(3 * time.Second)

    part(t, tag, nservers, []int{2,3,4}, []int{0,1}, []int{})

    // can majority partition make progress even though
    // minority servers were interrupted in the middle of
    // paxos agreements?
    check(t, ck2, "q", "q")
    ck2.Put("q", "qq")
    check(t, ck2, "q", "qq")
      
    // restore network, wait for all threads to exit.
    part(t, tag, nservers, []int{0,1,2,3,4}, []int{}, []int{})
    done = true
    ok := true
    for i := 0; i < nclients; i++ {
      z := <- ca[i]
      ok = ok && z
    }
    if ok == false {
      t.Fatal("something is wrong")
    }
    check(t, ck2, "q", "qq")
  }

  fmt.Printf("OK\n")
}

func TestManyPartition(t *testing.T) {
  runtime.GOMAXPROCS(4)

  fmt.Printf("Many clients, changing partitions: ")

  tag := "many"
  const nservers = 5
  var kva []*KVPaxos = make([]*KVPaxos, nservers)
  defer cleanup(kva)

  for i := 0; i < nservers; i++ {
    var kvh []string = make([]string, nservers)
    for j := 0; j < nservers; j++ {
      if j == i {
        kvh[j] = port(tag, i)
      } else {
        kvh[j] = pp(tag, i, j)
      }
    }
    kva[i] = StartServer(kvh, i)
    kva[i].unreliable = true
  }
  defer part(t, tag, nservers, []int{}, []int{}, []int{})
  part(t, tag, nservers, []int{0,1,2,3,4}, []int{}, []int{})

  done := false

  // re-partition periodically
  go func() {
    for done == false {
      var a [nservers]int
      for i := 0; i < nservers; i++ {
        a[i] = (rand.Int() % 3)
      }
      pa := make([][]int, 3)
      for i := 0; i < 3; i++ {
        pa[i] = make([]int, 0)
        for j := 0; j < nservers; j++ {
          if a[j] == i {
            pa[i] = append(pa[i], j)
          }
        }
      }
      part(t, tag, nservers, pa[0], pa[1], pa[2])
      time.Sleep(time.Duration(rand.Int63() % 200) * time.Millisecond)
    }
  }()

  const nclients = 10
  var ca [nclients]chan bool
  for xcli := 0; xcli < nclients; xcli++ {
    ca[xcli] = make(chan bool)
    go func(cli int) {
      ok := false
      defer func() { ca[cli] <- ok }()
      sa := make([]string, nservers)
      for i := 0; i < nservers; i++ {
        sa[i] = port(tag, i)
      }
      for i := range sa {
        j := rand.Intn(i+1)
        sa[i], sa[j] = sa[j], sa[i]
      }
      myck := MakeClerk(sa)
      key := strconv.Itoa(cli)
      last := ""
      myck.Put(key, last)
      for done == false {
        if (rand.Int() % 1000) < 500 {
          nv := strconv.Itoa(rand.Int())
          myck.Put(key, nv)
          last = nv
        } else {
          v := myck.Get(key)
          if v != last {
            t.Fatalf("%v: wrong value, key %v, wanted %v, got %v",
              cli, key, last, v)
          }
        }
      }
      ok = true
    } (xcli)
  }

  time.Sleep(10 * time.Second)
  done = true
  part(t, tag, nservers, []int{0,1,2,3,4}, []int{}, []int{})

  ok := true
  for i := 0; i < nclients; i++ {
    z := <- ca[i]
    ok = ok && z
  }

  if ok {
    fmt.Printf("OK\n")
  }
}