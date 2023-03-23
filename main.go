package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	/*"os"*/
	"sync"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	pstore "github.com/libp2p/go-libp2p/core/peerstore"
	drouting "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	dutil "github.com/libp2p/go-libp2p/p2p/discovery/util"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	maddr "github.com/multiformats/go-multiaddr"
	"github.com/davecgh/go-spew/spew"

	"github.com/ipfs/go-log/v2"

	mrand "math/rand"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
)

// A new type we need for writing a custom flag parser
type addrList []maddr.Multiaddr
const NODE_STEP = 60

func (al *addrList) String() string {
	strs := make([]string, len(*al))
	for i, addr := range *al {
		strs[i] = addr.String()
	}
	return strings.Join(strs, ",")
}

func (al *addrList) Set(value string) error {
	addr, err := maddr.NewMultiaddr(value)
	if err != nil {
		return err
	}
	*al = append(*al, addr)
	return nil
}

func StringsToAddrs(addrStrings []string) (maddrs []maddr.Multiaddr, err error) {
	for _, addrString := range addrStrings {
		addr, err := maddr.NewMultiaddr(addrString)
		if err != nil {
			return maddrs, err
		}
		maddrs = append(maddrs, addr)
	}
	return
}

type Config struct {
	RendezvousString string
	BootstrapPeers   addrList
	ListenAddresses  addrList
	ProtocolID       string
}

func ParseFlags() (Config, error) {
	config := Config{}
	flag.StringVar(&config.RendezvousString, "gitbank", "meet me here",
		"Unique string to identify group of nodes. Share this with your friends to let them connect with you")
	flag.Var(&config.BootstrapPeers, "peer", "Adds a peer multiaddress to the bootstrap list")
	flag.Var(&config.ListenAddresses, "listen", "Adds a multiaddress to the listen list")
	flag.StringVar(&config.ProtocolID, "pid", "/gitbank/1.0.0", "Sets a protocol id for stream headers")
	flag.Parse()

	/*if len(config.BootstrapPeers) == 0 {
		config.BootstrapPeers = dht.DefaultBootstrapPeers
	}*/

	return config, nil
}

type Block struct {
	Index     int
	Timestamp int64
	BPM       int
	Hash      string
	PrevHash  string
}

var Blockchain []Block

var mutex = &sync.Mutex{}

var logger = log.Logger("gitbank")

func handleStream(stream network.Stream) {
	logger.Info("Got a new stream!")

	// Create a buffer stream for non blocking read and write.
	rw := bufio.NewReadWriter(bufio.NewReader(stream), bufio.NewWriter(stream))

	go readData(rw)
	go writeData(rw)

	// 'stream' will stay open until you close it (or the other side closes it).
}

func processData(str string) (error){
	chain := make([]Block, 0)
	if err := json.Unmarshal([]byte(str), &chain); err != nil {
		logger.Fatal(err)
	}

	mutex.Lock()
	if int64(len(chain)) > int64(len(Blockchain)) && int64(len(chain)) <= int64((((time.Now().UTC().Unix() - time.Date(2023, 3, 23, 14, 46, 00, 000, time.UTC).Unix()) / 60) / NODE_STEP)) {
		Blockchain = chain
		bytes, err := json.MarshalIndent(Blockchain, "", "  ")
		if err != nil {

			logger.Fatal(err)
		}
		// Green console color: 	\x1b[32m
		// Reset console color: 	\x1b[0m
		fmt.Printf("\x1b[32m%s\x1b[0m> ", string(bytes))
	}
	
	mutex.Unlock()

	return nil
}

func readData(rw *bufio.ReadWriter) {
	for {
		str, err := rw.ReadString('\n')
		if err != nil {
		}else{
			if str == "" {
				return
			}
			if str != "\n" {
				err := processData(str)
				if err != nil {
					logger.Info(err)
					fmt.Println("Error processing data")
				}
			}
		}
	}
}

func writeData(rw *bufio.ReadWriter) {
	go func() {
		for {
			time.Sleep(5 * time.Second)
			mutex.Lock()
			bytes, err := json.Marshal(Blockchain)
			if err != nil {
				logger.Info(err)
			}
			mutex.Unlock()

			mutex.Lock()
			rw.WriteString(fmt.Sprintf("%s\n", string(bytes)))
			rw.Flush()
			mutex.Unlock()

		}
	}()

	for {
		time.Sleep(NODE_STEP * time.Second)

		newBlock := generateBlock(Blockchain[len(Blockchain)-1], 0)

		if isBlockValid(newBlock, Blockchain[len(Blockchain)-1]) {
			mutex.Lock()
			Blockchain = append(Blockchain, newBlock)
			mutex.Unlock()
		}

		bytes, err := json.Marshal(Blockchain)
		if err != nil {
			logger.Info(err)
		}

		spew.Dump(Blockchain)

		mutex.Lock()
		rw.WriteString(fmt.Sprintf("%s\n", string(bytes)))
		rw.Flush()
		mutex.Unlock()
	}
}

func makeBasicHost(listenPort int, secio bool, randseed int64,config Config) (host.Host, error) {
	// If the seed is zero, use real cryptographic randomness. Otherwise, use a
	// deterministic randomness source to make generated keys stay the same
	// across multiple runs
	var r io.Reader
	if randseed == 0 {
		r = rand.Reader
	} else {
		r = mrand.New(mrand.NewSource(randseed))
	}

	// Generate a key pair for this host. We will use it
	// to obtain a valid host ID.
	priv, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, r)
	if err != nil {
		return nil, err
	}

	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", listenPort)),
		libp2p.Identity(priv),
	}

	basicHost, err := libp2p.New(opts...)
	if err != nil {
		return nil, err
	}

	// Build host multiaddress
	hostAddr, _ := maddr.NewMultiaddr(fmt.Sprintf("/ipfs/%s", basicHost.ID().Pretty()))

	// Now we can build a full multiaddress to reach this host
	// by encapsulating both addresses:
	addrs := basicHost.Addrs()
	var addr maddr.Multiaddr
	// select the address starting with "ip4"
	for _, i := range addrs {
		if strings.HasPrefix(i.String(), "/ip4") {
			addr = i
			break
		}
	}
	fullAddr := addr.Encapsulate(hostAddr)
	logger.Info("I am " + fullAddr.String() + "\n")
	if secio {
		logger.Info("Now run \"go run main.go -l " + strconv.Itoa(listenPort+1) + " -d " + fullAddr.String() + " -secio\" on a different terminal\n")
	} else {
		logger.Info("Now run \"go run main.go -l " + strconv.Itoa(listenPort+1) + " -d " + fullAddr.String() + "\" on a different terminal\n")
	}

	return basicHost, nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	t := time.Now().UTC()
	genesisBlock := Block{}
	genesisBlock = Block{0, t.Unix(), 0, calculateHash(genesisBlock), ""}

	Blockchain = append(Blockchain, genesisBlock)

	log.SetAllLoggers(log.LevelWarn)
	log.SetLogLevel("gitbank", "info")
	help := flag.Bool("h", false, "Display Help")
	listenF := flag.Int("l", 0, "wait for incoming connections")
	peerAddrP := flag.String("d", "", "peer")
	config, err := ParseFlags()
	if err != nil {
		panic(err)
	}

	if *help {
		fmt.Println("This program demonstrates a simple p2p chat application using libp2p")
		fmt.Println()
		fmt.Println("Usage: Run './gitbank in two different terminals. Let them connect to the bootstrap nodes, announce themselves and connect to the peers")
		flag.PrintDefaults()
		return
	}

	// libp2p.New constructs a new libp2p Host. Other options can be added
	// here.
	if *listenF == 0 {
		logger.Fatal("Please provide a port to bind on with -l")
	}

	host, err := makeBasicHost(*listenF, false, 0, config)
	if err != nil {
		logger.Fatal(err)
	}
	logger.Info("Host created. We are:", host.ID())
	logger.Info(host.Addrs())

	// Set a function as stream handler. This function is called when a peer
	// initiates a connection and starts a stream with this peer.
	host.SetStreamHandler(protocol.ID(config.ProtocolID), handleStream)

	if len(*peerAddrP) > 0 {
		peerAddr, err := maddr.NewMultiaddr(*peerAddrP)
		if err != nil {
			logger.Fatal(err)
		}
		config.BootstrapPeers = append(config.BootstrapPeers, peerAddr)
	}

	var options []dht.Option
	if len(config.BootstrapPeers) == 0 {
		options = append(options, dht.Mode(dht.ModeServer))
	}

	// Start a DHT, for use in peer discovery. We can't just make a new DHT
	// client because we want each peer to maintain its own local copy of the
	// DHT, so that the bootstrapping node of the DHT can go down without
	// inhibiting future peer discovery.
	kademliaDHT, err := dht.New(ctx, host, options...)
	if err != nil {
		panic(err)
	}

	// Bootstrap the DHT. In the default configuration, this spawns a Background
	// thread that will refresh the peer table every five minutes.
	logger.Debug("Bootstrapping the DHT")
	if err = kademliaDHT.Bootstrap(ctx); err != nil {
		panic(err)
	}

	// Let's connect to the bootstrap nodes first. They will tell us about the
	// other nodes in the network.
	var wg sync.WaitGroup
	for _, peerAddr := range config.BootstrapPeers {
		peerinfo, _ := peer.AddrInfoFromP2pAddr(peerAddr)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := host.Connect(ctx, *peerinfo); err != nil {
				logger.Warning(err)
			} else {
				logger.Info("Connection established with bootstrap node:", *peerinfo)
			}
		}()
	}
	wg.Wait()

	// We use a rendezvous point "meet me here" to announce our location.
	// This is like telling your friends to meet you at the Eiffel Tower.
	logger.Info("Announcing ourselves...")
	routingDiscovery := drouting.NewRoutingDiscovery(kademliaDHT)
	dutil.Advertise(ctx, routingDiscovery, config.RendezvousString)
	logger.Debug("Successfully announced!")

	// Now, look for others who have announced
	// This is like your friend telling you the location to meet you.
	logger.Debug("Searching for other peers...")
	peerChan, err := routingDiscovery.FindPeers(ctx, config.RendezvousString)
	if err != nil {
		panic(err)
	}

	for peer := range peerChan {
		if peer.ID == host.ID() {
			continue
		}
		logger.Debug("Found peer:", peer)

		logger.Debug("Connecting to:", peer)
		stream, err := host.NewStream(ctx, peer.ID, protocol.ID(config.ProtocolID))

		if err != nil {
			logger.Warning("Connection failed:", err)
			continue
		} else {
			rw := bufio.NewReadWriter(bufio.NewReader(stream), bufio.NewWriter(stream))

			go writeData(rw)
			go readData(rw)
		}

		logger.Info("Connected to:", peer)

		host.Peerstore().AddAddr(peer.ID, peer.Addrs[0], pstore.PermanentAddrTTL)
	}

	select {}
}

// make sure block is valid by checking index, and comparing the hash of the previous block
func isBlockValid(newBlock, oldBlock Block) bool {
	if oldBlock.Index+1 != newBlock.Index {
		return false
	}

	if oldBlock.Hash != newBlock.PrevHash {
		return false
	}

	if calculateHash(newBlock) != newBlock.Hash {
		return false
	}

	return true
}

// SHA256 hashing
func calculateHash(block Block) string {
	record := strconv.Itoa(block.Index) + strconv.FormatInt(block.Timestamp, 10) + strconv.Itoa(block.BPM) + block.PrevHash
	h := sha256.New()
	h.Write([]byte(record))
	hashed := h.Sum(nil)
	return hex.EncodeToString(hashed)
}

// create a new block using previous block's hash
func generateBlock(oldBlock Block, BPM int) Block {

	var newBlock Block

	t := time.Now().UTC()

	newBlock.Index = oldBlock.Index + 1
	newBlock.Timestamp = t.Unix()
	newBlock.BPM = BPM
	newBlock.PrevHash = oldBlock.Hash
	newBlock.Hash = calculateHash(newBlock)

	return newBlock
}