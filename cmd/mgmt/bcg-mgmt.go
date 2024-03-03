package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/grandcat/zeroconf"
	"github.com/ibiscum/bobcaygeon/cmd/mgmt/raft"

	petname "github.com/dustinkirkland/golang-petname"
	"github.com/hashicorp/memberlist"
	"github.com/ibiscum/bobcaygeon/cluster"
	"github.com/ibiscum/bobcaygeon/cmd/mgmt/api"
	toml "github.com/pelletier/go-toml"
	"google.golang.org/grpc"
)

var (
	configPath = flag.String("config", "bcg-mgmt.toml", "Path to the config file for the node")
)

type nodeConfig struct {
	APIPort     int    `toml:"api-port"`
	ClusterPort int    `toml:"cluster-port"`
	Name        string `toml:"name"`
}

type mgmtConfig struct {
	RaftPort   int    `toml:"raft-port"`
	StorageDir string `toml:"storage-dir"`
}

type conf struct {
	Node nodeConfig `toml:"node"`
	Mgmt mgmtConfig `toml:"mgmt"`
}

type memberHandler struct {
	store   *raft.DistributedStore
	service *raft.DistributedMgmtService
}

func newMemberHandler(ds *raft.DistributedStore, service *raft.DistributedMgmtService) *memberHandler {
	return &memberHandler{store: ds, service: service}
}

// NotifyJoin is invoked when a node is detected to have joined.
// The Node argument must not be modified.
func (m *memberHandler) NotifyJoin(node *memberlist.Node) {
	log.Println("Node Joined " + node.Name)
	meta := cluster.DecodeNodeMeta(node.Meta)
	if meta.NodeType == cluster.Mgmt {
		raftPort := cluster.DecodeNodeMeta(node.Meta).RaftPort
		raftJoinAddr := fmt.Sprintf("%s:%d", node.Addr.String(), raftPort)
		err := m.store.Join(node.Name, raftJoinAddr)
		if err != nil {
			log.Println("Problem joining distributed store: ", err)
		}
	}
	if meta.NodeType == cluster.Music {
		go m.service.HandleMusicNodeJoin(node)
	}

}

// NotifyLeave is invoked when a node is detected to have left.
// The Node argument must not be modified.
func (m *memberHandler) NotifyLeave(node *memberlist.Node) {
	log.Println("Node Left" + node.Name)
	meta := cluster.DecodeNodeMeta(node.Meta)
	if meta.NodeType == cluster.Mgmt {

	}
	if meta.NodeType == cluster.Music {
		go m.service.HandleMusicNodeLeave(node)
	}

}

// NotifyUpdate is invoked when a node is detected to have
// updated, usually involving the meta data. The Node argument
// must not be modified.
func (*memberHandler) NotifyUpdate(node *memberlist.Node) {
	log.Println("Node updated" + node.Name)

}

func main() {
	flag.Parse()
	log.Println(*configPath)
	configFile, err := ioutil.ReadFile(*configPath)
	// if we os.Open returns an error then handle it
	if err != nil {
		log.Fatal("Could not open config file: ", err)
	}

	config := conf{}
	err = toml.Unmarshal(configFile, &config)
	if err != nil {
		log.Fatal("Could parse open config file: ", err)
	}

	if config.Node.Name == "" {
		log.Println("Generating node name")
		config.Node.Name = petname.Generate(2, "-")
		updated, err := toml.Marshal(config)
		if err != nil {
			log.Fatal("Could not update config")
		}
		ioutil.WriteFile(*configPath, updated, 0644)
	}

	nodeName := config.Node.Name
	log.Printf("Starting management API node: %s\n", nodeName)
	metaData := &cluster.NodeMeta{NodeType: cluster.Mgmt, APIPort: config.Node.APIPort, RaftPort: config.Mgmt.RaftPort}
	c := memberlist.DefaultLANConfig()
	c.Name = nodeName
	c.BindPort = config.Node.ClusterPort
	c.AdvertisePort = config.Node.ClusterPort
	c.Delegate = cluster.Delegate{MetaData: metaData}

	list, err := memberlist.Create(c)

	entry := cluster.SearchForCluster()

	if entry != nil {
		log.Println("Joining cluster")
		_, err = list.Join([]string{fmt.Sprintf("%s:%d", entry.AddrIPv4[0].String(), entry.Port)})
		if err != nil {
			panic("Failed to join cluster: " + err.Error())
		}
	}
	// start broadcasting the service
	log.Println("broadcasting my join info")
	server, err := zeroconf.Register(nodeName, cluster.ServiceType, "local.", config.Node.ClusterPort, []string{"txtv=0", "lo=1", "la=2"}, nil)
	if err != nil {
		log.Println("Error starting zeroconf service", err)
	}
	defer server.Shutdown()

	store := initDistributedStore(list, config.Node.Name, config.Mgmt.RaftPort, config.Mgmt.StorageDir)
	service := raft.NewDistributedMgmtService(list, store)
	// sets up the delegate to handle when members join or leave
	c.Events = cluster.NewEventDelegate([]memberlist.EventDelegate{newMemberHandler(store, service)})
	go startAPIServer(config.Node.APIPort, list, service)
	// Clean exit.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sig:
		// Exit by user
		log.Println("Ctrl-c detected, shutting down")
	}

	log.Println("Goodbye.")

}

func startAPIServer(apiServerPort int, list *memberlist.Memberlist, service *raft.DistributedMgmtService) {
	// create a listener
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", apiServerPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := api.NewServer(service)
	// create a gRPC server object
	grpcServer := grpc.NewServer()
	api.RegisterBobcaygeonManagementServer(grpcServer, s)
	log.Printf("Starting API server on port: %d", apiServerPort)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %s", err)
	}
}

func initDistributedStore(list *memberlist.Memberlist, localID string, raftPort int, raftDir string) *raft.DistributedStore {
	numMgmtNodes := len(cluster.FilterMembers(cluster.Mgmt, list))
	store := raft.NewDistributedStore(localID, raftPort, raftDir)
	// if there is only 1 mgmt node, it means we are the only one, so we will bootstrap cluster
	err := store.Open(numMgmtNodes == 1)
	if err != nil {
		log.Fatalf("failed to open database: %s", err)
	}
	return store
}
