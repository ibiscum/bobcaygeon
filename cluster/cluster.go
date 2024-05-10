package cluster

import (
	"bytes"
	"context"
	"encoding/gob"
	"log"
	"time"

	"github.com/grandcat/zeroconf"
	"github.com/hashicorp/memberlist"
)

// NodeType describes what type of role this node has in the cluster
type NodeType int

// MemberFilter function used to filter the list down
type MemberFilter func(*memberlist.Node) bool

const (
	// Music this node is responsible for music
	Music NodeType = iota
	// Mgmt this node is responsible for management
	Mgmt
	// Frontend this is a node for controlling front proxy
	Frontend
)

const (
	// ServiceType is the type used to advertise the cluster to join
	ServiceType = "_bobcaygeon._tcp"
)

// NodeMeta is metadata passed to other members about this node
type NodeMeta struct {
	RtspPort int
	APIPort  int
	RaftPort int
	NodeType NodeType
}

// EventDelegate handles the delgate functions from the memberlist
type EventDelegate struct {
	// keep a list of delegates so that we can have more than one
	// interested party for the membership events
	eventDelegates []memberlist.EventDelegate
}

// Delegate handles memberlist events
type Delegate struct {
	MetaData *NodeMeta
}

// NodeMeta is used to retrieve meta-data about the current node
// when broadcasting an alive message.
func (d Delegate) NodeMeta(limit int) []byte {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	err := enc.Encode(d.MetaData)
	if err != nil {
		log.Println("Error encoding node metadata", err)
	}

	return buf.Bytes()
}

// GetBroadcasts is called when user data messages can be broadcast.
func (Delegate) GetBroadcasts(overhead, limit int) [][]byte {
	return make([][]byte, 0)
}

// LocalState is used for a TCP Push/Pull. This is sent to
// the remote side in addition to the membership information.
func (Delegate) LocalState(join bool) []byte {
	return make([]byte, 0)
}

// MergeRemoteState is invoked after a TCP Push/Pull.
func (Delegate) MergeRemoteState(buf []byte, join bool) {}

// NotifyMsg is called when a user-data message is received.
func (Delegate) NotifyMsg([]byte) {}

// NewEventDelegate instantiates a new EventDelegate struct
func NewEventDelegate(d []memberlist.EventDelegate) *EventDelegate {
	return &EventDelegate{eventDelegates: d}
}

// NotifyJoin is invoked when a node is detected to have joined.
// The Node argument must not be modified.
func (ed *EventDelegate) NotifyJoin(node *memberlist.Node) {
	for _, delegate := range ed.eventDelegates {
		delegate.NotifyJoin(node)
	}
}

// NotifyLeave is invoked when a node is detected to have left.
// The Node argument must not be modified.
func (ed *EventDelegate) NotifyLeave(node *memberlist.Node) {
	for _, delegate := range ed.eventDelegates {
		delegate.NotifyLeave(node)
	}
}

// NotifyUpdate is invoked when a node is detected to have
// updated, usually involving the meta data. The Node argument
// must not be modified.
func (ed *EventDelegate) NotifyUpdate(node *memberlist.Node) {
	for _, delegate := range ed.eventDelegates {
		delegate.NotifyUpdate(node)
	}
}

// DecodeNodeMeta decodes node meta data from bytes into something useful
func DecodeNodeMeta(nodeMeta []byte) NodeMeta {
	dec := gob.NewDecoder(bytes.NewReader(nodeMeta))
	var meta NodeMeta
	err := dec.Decode(&meta)
	if err != nil {
		log.Fatal(err)
	}
	return meta
}

// FilterMembers filters down the memberlist to return only nodes of the given type
func FilterMembers(memberType NodeType, list *memberlist.Memberlist) []*memberlist.Node {
	var nodes []*memberlist.Node
	for _, member := range list.Members() {
		meta := DecodeNodeMeta(member.Meta)
		if meta.NodeType == memberType {
			nodes = append(nodes, member)
		}
	}
	return nodes
}

// FilterMembersByFn provides more flexibility in using a filtering function
func FilterMembersByFn(filter MemberFilter, list *memberlist.Memberlist) []*memberlist.Node {
	var nodes []*memberlist.Node
	for _, member := range list.Members() {
		if filter(member) {
			nodes = append(nodes, member)
		}
	}
	return nodes
}

// SearchForCluster searches for a cluster to join
func SearchForCluster() *zeroconf.ServiceEntry {
	// next we use mdns to try to find a cluster to join.
	// the curent leader (and receiving airplay server)
	// will be broadcasting a service to join
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		log.Fatalln("Failed to initialize resolver:", err.Error())
	}

	entries := make(chan *zeroconf.ServiceEntry)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(5))
	defer cancel()
	err = resolver.Browse(ctx, ServiceType, "local", entries)
	if err != nil {
		log.Fatalln("Failed to browse:", err.Error())
	}
	log.Println("searching for cluster to join")
	var entry *zeroconf.ServiceEntry
	foundEntry := make(chan *zeroconf.ServiceEntry)
	// what we do is spin of a goroutine that will process the entries registered in
	// mDNS for our service.  As soon as we detect there is one with an IP4 address
	// we send it off and cancel to stop the searching.
	// there is an issue, https://github.com/grandcat/zeroconf/issues/27 where we
	// could get an entry back without an IP4 addr, it will come in later as an update
	// so we wait until we find the addr, or timeout
	go func(results <-chan *zeroconf.ServiceEntry, foundEntry chan *zeroconf.ServiceEntry) {
		for e := range results {
			if (len(e.AddrIPv4)) > 0 {
				foundEntry <- e
				cancel()
			}
		}
	}(entries, foundEntry)

	select {
	// this should be ok, since we only expect one service of the _bobcaygeon_ type to be found
	case entry = <-foundEntry:
		log.Println("Found cluster to join")
	case <-ctx.Done():
		log.Println("cluster search timeout, no cluster to join")
	}

	return entry
}
