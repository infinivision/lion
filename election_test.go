package prophet

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/stretchr/testify/assert"
)

type electorTester struct {
	sync.Mutex

	group     uint64
	id        string
	leader    bool
	elector   Elector
	cancel    context.CancelFunc
	ctx       context.Context
	startedC  chan interface{}
	once      sync.Once
	blockTime time.Duration
}

func newElectorTester(group uint64, id string, elector Elector) *electorTester {
	ctx, cancel := context.WithCancel(context.Background())

	return &electorTester{
		startedC: make(chan interface{}),
		group:    group,
		id:       id,
		elector:  elector,
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (t *electorTester) start() {
	go t.elector.ElectionLoop(t.ctx, t.group, t.id, t.becomeLeader, t.becomeFollower)
	<-t.startedC
}

func (t *electorTester) stop(blockTime time.Duration) {
	t.blockTime = blockTime
	t.cancel()
}

func (t *electorTester) isLeader() bool {
	t.Lock()
	defer t.Unlock()

	return t.leader
}

func (t *electorTester) becomeLeader() {
	t.Lock()
	defer t.Unlock()

	t.leader = true
	t.notifyStarted()

	if t.blockTime > 0 {
		<-time.After(t.blockTime)
	}
}

func (t *electorTester) becomeFollower() {
	t.Lock()
	defer t.Unlock()

	t.leader = false
	t.notifyStarted()

	if t.blockTime > 0 {
		<-time.After(t.blockTime)
	}
}

func (t *electorTester) notifyStarted() {
	t.once.Do(func() {
		if t.startedC != nil {
			close(t.startedC)
			t.startedC = nil
		}
	})
}

func TestElectionLoop(t *testing.T) {
	stopC, err := startTestSingleEtcd(2379, 2380)
	assert.Nil(t, err, "start embed etcd failed")
	defer close(stopC)

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split("http://127.0.0.1:2379", ","),
		DialTimeout: DefaultTimeout,
	})
	assert.Nil(t, err, "create etcd client failed")

	elector, err := NewElector(client, WithLeaderLeaseSeconds(1))
	assert.Nil(t, err, "create elector failed")

	value1 := newElectorTester(0, "1", elector)
	value1.start()
	assert.True(t, value1.isLeader(), "value1 must be leader")

	value2 := newElectorTester(0, "2", elector)
	value2.start()
	assert.False(t, value2.isLeader(), "value2 must be follower")

	value1.stop(0)
	time.Sleep(time.Second)
	assert.True(t, value2.isLeader(), "value2 must be leader")
}

func TestElectionLoopWithDistributedLock(t *testing.T) {
	stopC, err := startTestSingleEtcd(2379, 2380)
	assert.Nil(t, err, "start embed etcd failed")
	defer close(stopC)

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split("http://127.0.0.1:2379", ","),
		DialTimeout: DefaultTimeout,
	})
	assert.Nil(t, err, "create etcd client failed")

	elector, err := NewElector(client,
		WithLeaderLeaseSeconds(1),
		WithLockIfBecomeLeader(true))
	assert.Nil(t, err, "create elector failed")

	value1 := newElectorTester(0, "1", elector)
	value1.start()
	assert.True(t, value1.isLeader(), "value1 must be leader")

	value2 := newElectorTester(0, "2", elector)
	value2.start()
	assert.False(t, value2.isLeader(), "value2 must be follower")

	value1.stop(time.Second * 2)
	time.Sleep(time.Second)
	assert.False(t, value2.isLeader(), "value2 must be follower before distributed lock released")

	time.Sleep(time.Second + time.Millisecond*100)
	assert.True(t, value2.isLeader(), "value2 must be leader after distributed lock released")
}

func TestChangeLeaderTo(t *testing.T) {
	stopC, err := startTestSingleEtcd(2379, 2380)
	assert.Nil(t, err, "start embed etcd failed")
	defer close(stopC)

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split("http://127.0.0.1:2379", ","),
		DialTimeout: DefaultTimeout,
	})
	assert.Nil(t, err, "create etcd client failed")

	elector, err := NewElector(client,
		WithLeaderLeaseSeconds(1))
	assert.Nil(t, err, "create elector failed")

	value1 := newElectorTester(0, "1", elector)
	value2 := newElectorTester(0, "2", elector)

	value1.start()
	value2.start()

	err = elector.ChangeLeaderTo(0, "2", "3")
	assert.NotNil(t, err, "only leader node can transfer leader")

	err = elector.ChangeLeaderTo(0, "1", "2")
	assert.Nil(t, err, "change leader failed")

	time.Sleep(time.Second)
	assert.False(t, value1.isLeader(), "value1 must be follower")
	assert.True(t, value2.isLeader(), "value2 must be leader")
}

func TestCurrentLeader(t *testing.T) {
	stopC, err := startTestSingleEtcd(2379, 2380)
	assert.Nil(t, err, "start embed etcd failed")
	defer close(stopC)

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split("http://127.0.0.1:2379", ","),
		DialTimeout: DefaultTimeout,
	})
	assert.Nil(t, err, "create etcd client failed")

	elector, err := NewElector(client,
		WithLeaderLeaseSeconds(1))
	assert.Nil(t, err, "create elector failed")

	value1 := newElectorTester(0, "1", elector)
	value2 := newElectorTester(0, "2", elector)

	value1.start()
	value2.start()

	data, err := elector.CurrentLeader(0)
	assert.Nil(t, err, "get current leader failed")
	assert.Equal(t, "1", string(data), "current leader failed")

	elector.ChangeLeaderTo(0, "1", "2")
	assert.Nil(t, err, "get current leader failed")

	time.Sleep(time.Second*1 + time.Millisecond*200)
	data, err = elector.CurrentLeader(0)
	assert.Nil(t, err, "get current leader failed")
	assert.Equalf(t, "2", string(data), "current leader failed, %+v", value2.isLeader())
}

func TestStop(t *testing.T) {
	stopC, err := startTestSingleEtcd(2379, 2380)
	assert.Nil(t, err, "start embed etcd failed")
	defer close(stopC)

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split("http://127.0.0.1:2379", ","),
		DialTimeout: DefaultTimeout,
	})
	assert.Nil(t, err, "create etcd client failed")

	elector, err := NewElector(client,
		WithLeaderLeaseSeconds(1))
	assert.Nil(t, err, "create elector failed")

	value1 := newElectorTester(0, "1", elector)
	value2 := newElectorTester(0, "2", elector)

	value1.start()
	value2.start()

	elector.Stop(0)

	time.Sleep(time.Second + time.Microsecond*200)
	assert.False(t, value1.isLeader(), "value1 must be follower")
}