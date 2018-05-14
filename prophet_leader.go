package prophet

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"
)

var (
	loopInterval = 200 * time.Millisecond
)

// Node is prophet info
type Node struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
	Addr string `json:"addr"`
}

func (n *Node) marshal() string {
	data, _ := json.Marshal(n)
	return string(data)
}

func (p *Prophet) startLeaderLoop() {
	p.runner.RunCancelableTask(func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				log.Infof("prophet: exit the leader election loop")
				return
			default:
				leader, err := p.store.GetCurrentLeader()
				if err != nil {
					log.Errorf("prophet: get current leader failure, errors:\n %+v",
						err)
					time.Sleep(loopInterval)
					continue
				}

				if leader != nil {
					if p.isMatchLeader(leader) {
						// oh, we are already leader, we may meet something wrong
						// in previous campaignLeader. we can resign and campaign again.
						log.Warnf("prophet: leader is matched, resign and campaign again, leader is <%v>",
							leader)
						if err = p.store.ResignLeader(p.signature); err != nil {
							log.Warnf("prophet: resign leader failure, leader <%v>, errors:\n %+v",
								leader,
								err)
							time.Sleep(loopInterval)
							continue
						}
					} else {
						log.Infof("prophet: we are not leader, watch the leader <%v>",
							leader)
						p.leader = leader // reset leader node for forward
						p.notifyElectionComplete()
						p.store.WatchLeader()
						log.Infof("prophet: leader changed, try to campaign leader <%v>", leader)
					}
				}

				log.Debugf("prophet: begin to campaign leader %s",
					p.node.Name)
				if err = p.store.CampaignLeader(p.signature, p.opts.leaseTTL, p.enableLeader, p.disableLeader); err != nil {
					log.Errorf("prophet: campaign leader failure, errors:\n %+v", err)
				}
			}
		}
	})
	<-p.completeC

}

func (p *Prophet) enableLeader() {
	// now, we are leader
	atomic.StoreInt64(&p.leaderFlag, 1)
	log.Infof("prophet: ********become to leader now********")

	p.rt = newRuntime(p.store)
	p.rt.load()

	p.notifyElectionComplete()
}

func (p *Prophet) disableLeader() {
	// now, we are not leader
	atomic.StoreInt64(&p.leaderFlag, 0)
	log.Infof("prophet: ********become to follower now********")
}

func (p *Prophet) isLeader() bool {
	return 0 == atomic.LoadInt64(&p.leaderFlag)
}

func (p *Prophet) notifyElectionComplete() {
	if p.completeC != nil {
		p.completeC <- struct{}{}
	}
}

func (p *Prophet) isMatchLeader(leaderNode *Node) bool {
	return leaderNode != nil &&
		p.node.Name == leaderNode.Name &&
		p.node.ID == leaderNode.ID
}
