package f3

import (
	"slices"
	"sync"

	"github.com/filecoin-project/go-f3/gpbft"
	"github.com/libp2p/go-libp2p/core/peer"
)

// zero value is valid
type equivocationFilter struct {
	lk              sync.Mutex
	localPID        peer.ID
	currentInstance uint64
	// seenMessages map unique message slot to its signature
	seenMessages  map[equivocationKey]equivMessage
	activeSenders map[gpbft.ActorID]equivSenders
}

func newEquivocationFilter(localPID peer.ID) equivocationFilter {
	return equivocationFilter{
		localPID:      localPID,
		seenMessages:  make(map[equivocationKey]equivMessage),
		activeSenders: make(map[gpbft.ActorID]equivSenders),
	}
}

type equivocationKey struct {
	Sender gpbft.ActorID
	Round  uint64
	Phase  gpbft.Phase
}

func (ef *equivocationFilter) formKey(m *gpbft.GMessage) equivocationKey {
	return equivocationKey{
		Sender: m.Sender,
		Round:  m.Vote.Round,
		Phase:  m.Vote.Phase,
	}
}

type equivSenders struct {
	origins      []peer.ID
	equivocation bool
}

type equivMessage struct {
	signature []byte
	origin    peer.ID
}

func (es *equivSenders) addSender(id peer.ID, equivocation bool) {
	if !slices.Contains(es.origins, id) {
		es.origins = append(es.origins, id)
		if len(es.origins) > 10 {
			es.origins = es.origins[:10]
		}
		slices.Sort(es.origins)
	}
	es.equivocation = es.equivocation || equivocation
}

func (ef *equivocationFilter) ProcessBroadcast(m *gpbft.GMessage) bool {
	ef.lk.Lock()
	defer ef.lk.Unlock()

	if m.Vote.Instance < ef.currentInstance {
		// disallow past instances
		log.Warnw("disallowing broadcast for past instance", "sender", m.Sender, "instance",
			m.Vote.Instance, "currentInstance", ef.currentInstance)
		return false
	}
	// moved onto new instance
	if m.Vote.Instance > ef.currentInstance {
		ef.currentInstance = m.Vote.Instance
		ef.seenMessages = make(map[equivocationKey]equivMessage)
		ef.activeSenders = make(map[gpbft.ActorID]equivSenders)
	}

	// save ourselves as one of the senders
	senders := ef.activeSenders[m.Sender]
	senders.addSender(ef.localPID, false)
	ef.activeSenders[m.Sender] = senders

	// always allow broadcast
	return true
}

func (ef *equivocationFilter) ProcessReceive(peerID peer.ID, m *gpbft.GMessage) {
	ef.lk.Lock()
	defer ef.lk.Unlock()

	if m.Vote.Instance != ef.currentInstance {
		// the instance does not match
		return
	}
	_, ok := ef.activeSenders[m.Sender]
	if !ok {
		// we do not track the sender because we didn't send any messages from that ID
		// otherwise we would have to track all messages
		return
	}
	key := ef.formKey(m)
	_, ok = ef.seenMessages[key]
	if !ok {
		// add the message
		ef.seenMessages[key] = equivMessage{signature: m.Signature, origin: peerID}
	}
}
