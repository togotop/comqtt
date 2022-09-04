package raft

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"
	raftdb "github.com/hashicorp/raft-boltdb"
)

type Node struct {
	raft *raft.Raft
	fsm  *Fsm
}

func NewRaftNode(raftAddr, raftId, raftDir string) (*Node, error) {
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(raftId)
	// config.HeartbeatTimeout = 1000 * time.Millisecond
	// config.ElectionTimeout = 1000 * time.Millisecond
	// config.CommitTimeout = 1000 * time.Millisecond

	addr, err := net.ResolveTCPAddr("tcp", raftAddr)
	if err != nil {
		return nil, err
	}
	transport, err := raft.NewTCPTransport(raftAddr, addr, 2, 5*time.Second, os.Stderr)
	if err != nil {
		return nil, err
	}
	snapshots, err := raft.NewFileSnapshotStore(raftDir, 2, os.Stderr)
	if err != nil {
		return nil, err
	}
	logStore, err := raftdb.NewBoltStore(filepath.Join(raftDir, "raft-log.db"))
	if err != nil {
		return nil, err
	}
	stableStore, err := raftdb.NewBoltStore(filepath.Join(raftDir, "raft-stable.db"))
	if err != nil {
		return nil, err
	}
	fm := NewFsm()
	rf, err := raft.NewRaft(config, fm, logStore, stableStore, snapshots, transport)
	if err != nil {
		return nil, err
	}

	conf := raft.Configuration{
		Servers: []raft.Server{
			{
				ID:      config.LocalID,
				Address: transport.LocalAddr(),
			},
		},
	}
	rf.BootstrapCluster(conf)

	return &Node{rf, fm}, nil
}

func (n *Node) Shutdown() error {
	// 关闭raft
	shutdownFuture := n.raft.Shutdown()
	if err := shutdownFuture.Error(); err != nil {
		return errors.New(fmt.Sprintf("shutdown raft error:%v \n", err))
	}

	return nil
}

func (n *Node) SetListener(listener func(op, key, value string)) {
	n.fsm.listener = listener
}

func (n *Node) Apply(cmd []byte, timeout time.Duration) error {
	future := n.raft.Apply(cmd, timeout)
	if err := future.Error(); err != nil {
		return err
	}
	return nil
}

func (n *Node) Search(key string) []string {
	return n.fsm.Search(key)
}

func (n *Node) DelByNode(node string) int {
	return n.fsm.DelByNode(node)
}

func (n *Node) IsLeader() bool {
	return n.raft.State() == raft.Leader
}

func (n *Node) GetLeader() (addr, id string) {
	leaderAddr, leaderId := n.raft.LeaderWithID()
	addr = string(leaderAddr)
	id = string(leaderId)
	return
}

func (n *Node) Status() ([]byte, error) {
	return json.Marshal(n.raft.Stats())
}

func (n *Node) Join(nodeID, addr string) error {
	if n.raft.State() != raft.Leader {
		return errors.New("not leader cannot join a node")
	}

	cf := n.raft.GetConfiguration()
	if err := cf.Error(); err != nil {
		return errors.New("failed to get raft configuration")
	}

	for _, server := range cf.Configuration().Servers {
		if server.ID == raft.ServerID(nodeID) {
			return errors.New(fmt.Sprintf("node %s already joined raft cluster", nodeID))
		}
	}

	f := n.raft.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 0)
	if err := f.Error(); err != nil {
		return err
	}

	return nil
}

func (n *Node) Leave(nodeID string) error {
	if n.raft.State() != raft.Leader {
		return errors.New("not leader cannot remove a node")
	}

	cf := n.raft.GetConfiguration()
	if err := cf.Error(); err != nil {
		return errors.New("failed to get raft configuration")
	}

	for _, server := range cf.Configuration().Servers {
		if server.ID == raft.ServerID(nodeID) {
			f := n.raft.RemoveServer(server.ID, 0, 0)
			if err := f.Error(); err != nil {
				return errors.New(fmt.Sprintf("failed to remove server %s", nodeID))
			}
			return nil
		}
	}

	return nil
}

func (n *Node) Snapshot() error {
	f := n.raft.Snapshot()
	return f.Error()
}
