package daemon

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/boltdb/bolt"

	"github.com/samoslab/samos/src/cipher"
	"github.com/samoslab/samos/src/coin"
	"github.com/samoslab/samos/src/daemon/gnet"
	"github.com/samoslab/samos/src/daemon/strand"
	"github.com/samoslab/samos/src/util/utc"
	"github.com/samoslab/samos/src/visor"
)

//TODO
//- download block headers
//- request blocks individually across multiple peers

//TODO
//- use CXO for blocksync

/*
Visor should not be duplicated
- this should be pushed into /src/visor
*/

// VisorConfig represents the configuration of visor
type VisorConfig struct {
	Config visor.Config
	// Disable visor networking
	DisableNetworking bool
	// How often to request blocks from peers
	BlocksRequestRate time.Duration
	// How often to announce our blocks to peers
	BlocksAnnounceRate time.Duration
	// How many blocks to respond with to a GetBlocksMessage
	BlocksResponseCount uint64
	// How long between saving copies of the blockchain
	BlockchainBackupRate time.Duration
	// Max announce txns hash number
	MaxTxnAnnounceNum int
	// How often to announce our unconfirmed txns to peers
	TxnsAnnounceRate time.Duration
	// How long to wait for Visor request to process
	RequestDeadline time.Duration
	// Internal request buffer size
	RequestBufferSize int
	// How often to announce our trust node to peers
	TrustNodeAnnounceRate time.Duration
	TrustNodeRequestRate  time.Duration
	PrepareRequestRate    time.Duration
	AgreeNumRequestRate   time.Duration
}

// NewVisorConfig creates default visor config
func NewVisorConfig() VisorConfig {
	return VisorConfig{
		Config:                visor.NewVisorConfig(),
		DisableNetworking:     false,
		BlocksRequestRate:     time.Second * 60,
		BlocksAnnounceRate:    time.Second * 60,
		BlocksResponseCount:   20,
		BlockchainBackupRate:  time.Second * 30,
		MaxTxnAnnounceNum:     16,
		TxnsAnnounceRate:      time.Minute,
		RequestDeadline:       time.Second * 3,
		RequestBufferSize:     100,
		TrustNodeAnnounceRate: time.Second * 60,
		TrustNodeRequestRate:  time.Second * 60,
		PrepareRequestRate:    time.Second * 2,
		AgreeNumRequestRate:   time.Second * 60,
	}
}

// Visor struct
type Visor struct {
	Config VisorConfig
	v      *visor.Visor
	V      *visor.Visor
	// Peer-reported blockchain height.  Use to estimate download progress
	blockchainHeights map[string]uint64
	// all request will go through this channel, to keep writing and reading member variable thread safe.
	reqC chan strand.Request
	quit chan struct{}
}

// NewVisor creates visor instance
func NewVisor(c VisorConfig, db *bolt.DB) (*Visor, error) {
	vs := &Visor{
		Config:            c,
		blockchainHeights: make(map[string]uint64),
		reqC:              make(chan strand.Request, c.RequestBufferSize),
		quit:              make(chan struct{}),
	}

	v, err := visor.NewVisor(c.Config, db)
	if err != nil {
		return nil, err
	}

	vs.v = v
	vs.V = v

	return vs, nil
}

// Run starts the visor
func (vs *Visor) Run() error {
	defer logger.Info("Visor closed")
	errC := make(chan error, 1)
	go func() {
		errC <- vs.v.Run()
	}()

	return vs.processRequests(errC)
}

func (vs *Visor) processRequests(errC <-chan error) error {
	for {
		select {
		case err := <-errC:
			return err
		case req := <-vs.reqC:
			if err := req.Func(); err != nil {
				logger.Errorf("Visor request func failed: %v", err)
			}
		}
	}
}

// Shutdown shuts down the visor
func (vs *Visor) Shutdown() {
	close(vs.quit)
	vs.v.Shutdown()
}

func (vs *Visor) strand(name string, f func() error) error {
	name = fmt.Sprintf("daemon.Visor.%s", name)
	return strand.Strand(logger, vs.reqC, name, f, vs.quit, nil)
}

// RefreshUnconfirmed checks unconfirmed txns against the blockchain and marks
// and returns those that become valid
func (vs *Visor) RefreshUnconfirmed() ([]cipher.SHA256, error) {
	var hashes []cipher.SHA256
	if err := vs.strand("RefreshUnconfirmed", func() error {
		var err error
		hashes, err = vs.v.RefreshUnconfirmed()
		return err
	}); err != nil {
		return nil, err
	}
	return hashes, nil
}

// RemoveInvalidUnconfirmed checks unconfirmed txns against the blockchain and
// purges those that become permanently invalid, violating hard constraints
func (vs *Visor) RemoveInvalidUnconfirmed() ([]cipher.SHA256, error) {
	var hashes []cipher.SHA256
	if err := vs.strand("RemoveInvalidUnconfirmed", func() error {
		var err error
		hashes, err = vs.v.RemoveInvalidUnconfirmed()
		return err
	}); err != nil {
		return nil, err
	}
	return hashes, nil
}

// RequestBlocks Sends a GetBlocksMessage to all connections
func (vs *Visor) RequestBlocks(pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	err := vs.strand("RequestBlocks", func() error {
		m := NewGetBlocksMessage(vs.v.HeadBkSeq(), vs.Config.BlocksResponseCount)
		return pool.Pool.BroadcastMessage(m)
	})

	if err != nil {
		logger.Debugf("Broadcast GetBlocksMessage failed: %v", err)
	}

	return err
}

// AnnounceBlocks sends an AnnounceBlocksMessage to all connections
func (vs *Visor) AnnounceBlocks(pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	err := vs.strand("AnnounceBlocks", func() error {
		m := NewAnnounceBlocksMessage(vs.v.HeadBkSeq())
		return pool.Pool.BroadcastMessage(m)
	})

	if err != nil {
		logger.Debugf("Broadcast AnnounceBlocksMessage failed: %v", err)
	}

	return err
}

// RequestTrustNode Sends a GetTrustNodeMessage to all connections
func (vs *Visor) RequestTrustNode(pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	err := vs.strand("RequestTrustNode", func() error {
		m := NewGetTrustMessage()
		return pool.Pool.BroadcastMessage(m)
	})

	if err != nil {
		logger.Debugf("Broadcast GetTrustMessage failed: %v", err)
	}

	return err
}

// AnnounceTrustNode sends an AnnounceTrustNodeMessage to all connections
func (vs *Visor) AnnounceTrustNode(pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	err := vs.strand("AnnounceTrustNode", func() error {
		m := NewAnnounceTrustMessage(vs.v.TrustNodes())
		return pool.Pool.BroadcastMessage(m)
	})

	if err != nil {
		logger.Debugf("Broadcast AnnounceTrustMessage failed: %v", err)
	}

	return err
}

// AnnounceAllTxns announces local unconfirmed transactions
func (vs *Visor) AnnounceAllTxns(pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	err := vs.strand("AnnounceAllTxns", func() error {
		// Get local unconfirmed transaction hashes.
		hashes := vs.v.GetAllValidUnconfirmedTxHashes()

		// Divide hashes into multiple sets of max size
		hashesSet := divideHashes(hashes, vs.Config.MaxTxnAnnounceNum)

		for _, hs := range hashesSet {
			m := NewAnnounceTxnsMessage(hs)
			if err := pool.Pool.BroadcastMessage(m); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		logger.Debugf("Broadcast AnnounceTxnsMessage failed, err: %v", err)
	}

	return err
}

// AnnounceTxns announces given transaction hashes.
func (vs *Visor) AnnounceTxns(pool *Pool, txns []cipher.SHA256) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	if len(txns) == 0 {
		return nil
	}

	err := vs.strand("AnnounceTxns", func() error {
		m := NewAnnounceTxnsMessage(txns)
		return pool.Pool.BroadcastMessage(m)
	})

	if err != nil {
		logger.Debugf("Broadcast AnnounceTxnsMessage failed: %v", err)
	}

	return err
}

func divideHashes(hashes []cipher.SHA256, n int) [][]cipher.SHA256 {
	if len(hashes) == 0 {
		return [][]cipher.SHA256{}
	}

	var j int
	var hashesArray [][]cipher.SHA256

	if len(hashes) > n {
		for i := range hashes {
			if len(hashes[j:i]) == n {
				hs := make([]cipher.SHA256, n)
				copy(hs, hashes[j:i])
				hashesArray = append(hashesArray, hs)
				j = i
			}
		}
	}

	hs := make([]cipher.SHA256, len(hashes)-j)
	copy(hs, hashes[j:])
	hashesArray = append(hashesArray, hs)
	return hashesArray
}

// RequestBlocksFromAddr sends a GetBlocksMessage to one connected address
func (vs *Visor) RequestBlocksFromAddr(pool *Pool, addr string) error {
	if vs.Config.DisableNetworking {
		return errors.New("Visor disabled")
	}

	err := vs.strand("RequestBlocksFromAddr", func() error {
		m := NewGetBlocksMessage(vs.v.HeadBkSeq(), vs.Config.BlocksResponseCount)
		exist, err := pool.Pool.IsConnExist(addr)
		if err != nil {
			return err
		}

		if !exist {
			return fmt.Errorf("Tried to send GetBlocksMessage to %s, but we are not connected", addr)
		}

		return pool.Pool.SendMessage(addr, m)
	})

	return err
}

// SetTxnsAnnounced sets all txns as announced
func (vs *Visor) SetTxnsAnnounced(txns []cipher.SHA256) {
	vs.strand("SetTxnsAnnounced", func() error {
		now := utc.Now()
		for _, h := range txns {
			if err := vs.v.Unconfirmed.SetAnnounced(h, now); err != nil {
				logger.Error("Failed to set unconfirmed txn announce time: ", err)
			}
		}

		return nil
	})
}

// InjectBroadcastTransaction injects transaction to the unconfirmed pool and broadcasts it.
// If the transaction violates either hard or soft constraints, it is not broadcast.
// This method is to be used by user-initiated transaction injections.
// For transactions received over the network, use InjectTransaction and check the result to
// decide on repropagation.
func (vs *Visor) InjectBroadcastTransaction(txn coin.Transaction, pool *Pool) error {
	return vs.strand("InjectBroadcastTransaction", func() error {
		if _, err := vs.v.InjectTransactionStrict(txn); err != nil {
			return err
		}

		return vs.broadcastTransaction(txn, pool)
	})
}

// InjectTransaction adds a transaction to the unconfirmed txn pool if it does not violate hard constraints.
// The transaction is added to the pool if it only violates soft constraints.
// If a soft constraint is violated, the specific error is returned separately.
func (vs *Visor) InjectTransaction(tx coin.Transaction) (bool, *visor.ErrTxnViolatesSoftConstraint, error) {
	var known bool
	var softErr *visor.ErrTxnViolatesSoftConstraint
	err := vs.strand("InjectTransaction", func() error {
		var err error
		known, softErr, err = vs.v.InjectTransaction(tx)
		return err
	})
	return known, softErr, err
}

// Sends a signed block to all connections.
// TODO: deprecate, should only send to clients that request by hash
func (vs *Visor) broadcastBlock(sb coin.SignedBlock, pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	m := NewGiveBlocksMessage([]coin.SignedBlock{sb})
	return pool.Pool.BroadcastMessage(m)
}

// Sends a signed block to all connections.
// TODO: deprecate, should only send to clients that request by hash
func (vs *Visor) broadcastPendingBlock(sb coin.PendingSignedBlock, pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	m := NewGivePendingBlockMessage([]coin.PendingSignedBlock{sb})
	return pool.Pool.BroadcastMessage(m)
}

// Sends a prepare message to all connections
func (vs *Visor) broadcastPrepareMsg(sb coin.PendingSignedBlock, pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	m := NewGivePrepareMessage(sb.HashHeader(), vs.v.Config.BlockchainTrustSeckey)
	return pool.Pool.BroadcastMessage(m)
}

// BroadcastMessage message to all connections
func (vs *Visor) BroadcastMessage(pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	if vs.v.IsGenesisNode() {
		// Locate all txns from the unconfirmed pool
		trustNodes := vs.TrustNodes()
		m := NewGiveTrustMessage(trustNodes, vs.v.Config.BlockchainSeckey)
		if err := pool.Pool.BroadcastMessage(m); err != nil {
			logger.Errorf("Broadcast GiveTrustMessage failed: %v", err)
			return err
		}
		num := vs.v.GetAgreeNodeNum()
		if num > 0 && num <= len(trustNodes) {
			m := NewGiveAgreeNumMessage(num, vs.v.Config.BlockchainSeckey)
			return pool.Pool.BroadcastMessage(m)
		}
	}
	return nil
}

// broadcastTransaction broadcasts a single transaction to all peers.
func (vs *Visor) broadcastTransaction(t coin.Transaction, pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	m := NewGiveTxnsMessage(coin.Transactions{t})
	l, err := pool.Pool.Size()
	if err != nil {
		return err
	}

	logger.Debugf("Broadcasting GiveTxnsMessage to %d conns", l)

	err = pool.Pool.BroadcastMessage(m)
	if err != nil {
		logger.Errorf("Broadcast GivenTxnsMessage failed: %v", err)
	}

	return err
}

// ResendTransaction resends a known UnconfirmedTxn.
func (vs *Visor) ResendTransaction(h cipher.SHA256, pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	return vs.strand("ResendTransaction", func() error {
		if ut, ok := vs.v.Unconfirmed.Get(h); ok {
			return vs.broadcastTransaction(ut.Txn, pool)
		}
		return nil
	})
}

// ResendUnconfirmedTxns resents all unconfirmed transactions
func (vs *Visor) ResendUnconfirmedTxns(pool *Pool) []cipher.SHA256 {
	if vs.Config.DisableNetworking {
		return nil
	}

	var txids []cipher.SHA256
	vs.strand("ResendUnconfirmedTxns", func() error {
		txns := vs.v.GetAllUnconfirmedTxns()

		for i := range txns {
			logger.Debugf("Rebroadcast tx %s", txns[i].Hash().Hex())
			if err := vs.broadcastTransaction(txns[i].Txn, pool); err == nil {
				txids = append(txids, txns[i].Txn.Hash())
			}
		}

		return nil
	})
	return txids
}

// InTurnTheNode should the node create block
func (vs *Visor) InTurnTheNode(when int64) (bool, error) {
	return vs.v.InTurnTheNode(when)
}

// HasUnconfirmedBlock check is there unconfimed block
func (vs *Visor) HasUnconfirmedBlock() bool {
	return len(vs.v.GetPendingHash()) > 0
}

// CreateAndPublishBlock creates a block from unconfirmed transactions and sends it to the network.
// Will panic if not running as a master chain.  Returns creation error and
// whether it was published or not
func (vs *Visor) CreateAndPublishBlock(pool *Pool) (coin.PendingSignedBlock, error) {
	if vs.Config.DisableNetworking {
		return coin.PendingSignedBlock{}, errors.New("Visor disabled")
	}

	var sb coin.PendingSignedBlock
	err := vs.strand("CreateAndPublishBlock", func() error {
		var err error
		sb, err = vs.v.CreateAndExecuteBlock()
		if err != nil {
			return err
		}

		err = vs.broadcastPendingBlock(sb, pool)
		if err != nil {
			return err
		}
		return vs.broadcastPrepareMsg(sb, pool)
	})

	return sb, err
}

// RemoveConnection updates internal state when a connection disconnects
func (vs *Visor) RemoveConnection(addr string) {
	vs.strand("RemoveConnection", func() error {
		delete(vs.blockchainHeights, addr)
		return nil
	})
}

// RecordBlockchainHeight saves a peer-reported blockchain length
func (vs *Visor) RecordBlockchainHeight(addr string, bkLen uint64) {
	vs.strand("RecordBlockchainHeight", func() error {
		vs.blockchainHeights[addr] = bkLen
		return nil
	})
}

// EstimateBlockchainHeight returns the blockchain length estimated from peer reports
// Deprecate. Should not need. Just report time of last block
func (vs *Visor) EstimateBlockchainHeight() uint64 {
	var maxLen uint64
	vs.strand("EstimateBlockchainHeight", func() error {
		ourLen := vs.v.HeadBkSeq()
		if len(vs.blockchainHeights) < 2 {
			maxLen = ourLen
			return nil
		}

		for _, seq := range vs.blockchainHeights {
			if maxLen < seq {
				maxLen = seq
			}
		}

		return nil
	})
	return maxLen
}

// PeerBlockchainHeight is a peer's IP address with their reported blockchain height
type PeerBlockchainHeight struct {
	Address string
	Height  uint64
}

// GetPeerBlockchainHeights returns recorded peers' blockchain heights as an array.
func (vs *Visor) GetPeerBlockchainHeights() []PeerBlockchainHeight {
	var peerHeights []PeerBlockchainHeight
	vs.strand("GetPeerBlockchainHeights", func() error {
		if len(vs.blockchainHeights) == 0 {
			return nil
		}

		peerHeights = make([]PeerBlockchainHeight, 0, len(peerHeights))
		for addr, height := range vs.blockchainHeights {
			peerHeights = append(peerHeights, PeerBlockchainHeight{
				Address: addr,
				Height:  height,
			})
		}

		return nil
	})

	return peerHeights
}

// HeadBkSeq returns the head sequence
func (vs *Visor) HeadBkSeq() uint64 {
	var seq uint64
	vs.strand("HeadBkSeq", func() error {
		seq = vs.v.HeadBkSeq()
		return nil
	})
	return seq
}

// ExecuteSignedBlock executes signed block
func (vs *Visor) ExecuteSignedBlock(b coin.SignedBlock) error {
	return vs.strand("ExecuteSignedBlock", func() error {
		return vs.v.ExecuteSignedBlock(b)
	})
}

// GetSignedBlock returns a copy of signed block at seq.
// Returns error if seq is greater than blockhain height.
func (vs *Visor) GetSignedBlock(seq uint64) (*coin.SignedBlock, error) {
	var sb *coin.SignedBlock
	err := vs.strand("GetSignedBlock", func() error {
		var err error
		sb, err = vs.v.GetBlock(seq)
		return err
	})
	return sb, err
}

// GetSignedBlocksSince returns signed blocks in an inclusive range of [seq+1, seq+ct]
func (vs *Visor) GetSignedBlocksSince(seq uint64, ct uint64) ([]coin.SignedBlock, error) {
	var sbs []coin.SignedBlock
	err := vs.strand("GetSignedBlocksSince", func() error {
		var err error
		sbs, err = vs.v.GetSignedBlocksSince(seq, ct)
		return err
	})
	return sbs, err
}

// UnConfirmFilterKnown returns all unknown transaction hashes
func (vs *Visor) UnConfirmFilterKnown(txns []cipher.SHA256) []cipher.SHA256 {
	var ts []cipher.SHA256
	vs.strand("UnConfirmFilterKnown", func() error {
		ts = vs.v.Unconfirmed.FilterKnown(txns)
		return nil
	})
	return ts
}

// UnConfirmKnow returns all know tansactions
func (vs *Visor) UnConfirmKnow(hashes []cipher.SHA256) coin.Transactions {
	var txns coin.Transactions
	vs.strand("UnConfirmKnow", func() error {
		txns = vs.v.Unconfirmed.GetKnown(hashes)
		return nil
	})
	return txns
}

// TrustNodes returns all trust nodes
func (vs *Visor) TrustNodes() []cipher.PubKey {
	return vs.v.TrustNodes()
}

// AddPendingBlock add pending block
func (vs *Visor) AddPendingBlock(block coin.SignedBlock) error {
	return vs.v.AddPendingBlock(block)
}

func CanMakeBlock(dm *Daemon, hash cipher.SHA256) error {
	creatorNum := len(dm.Visor.V.TrustNodes())
	currentNum, err := dm.Visor.V.GetValidatorNumber(hash)
	if err != nil {
		logger.Errorf("Get Validator Number failed: %v", err)
		return err
	}
	agreeNum := dm.Visor.V.GetAgreeNodeNum()
	if agreeNum <= 0 || agreeNum > creatorNum {
		agreeNum = creatorNum
	}
	if currentNum >= agreeNum {
		err := dm.Visor.V.StartExecuteSignedBlock(hash)
		if err != nil {
			logger.Errorf("Start Execute Block %s failed: %v", hash, err)
			return err
		}
	}
	return nil
}

// Communication layer for the coin pkg

// GetBlocksMessage sent to request blocks since LastBlock
type GetBlocksMessage struct {
	LastBlock       uint64
	RequestedBlocks uint64
	c               *gnet.MessageContext `enc:"-"`
}

// NewGetBlocksMessage creates GetBlocksMessage
func NewGetBlocksMessage(lastBlock uint64, requestedBlocks uint64) *GetBlocksMessage {
	return &GetBlocksMessage{
		LastBlock:       lastBlock,
		RequestedBlocks: requestedBlocks, // count of blocks requested
	}
}

// Handle handles message
func (gbm *GetBlocksMessage) Handle(mc *gnet.MessageContext,
	daemon interface{}) error {
	gbm.c = mc
	return daemon.(*Daemon).recordMessageEvent(gbm, mc)
}

// Process should send number to be requested, with request
func (gbm *GetBlocksMessage) Process(d *Daemon) {
	// TODO -- we need the sig to be sent with the block, but only the master
	// can sign blocks.  Thus the sig needs to be stored with the block.
	// TODO -- move to either Messages.Config or Visor.Config
	if d.Visor.Config.DisableNetworking {
		return
	}
	// Record this as this peer's highest block
	d.Visor.RecordBlockchainHeight(gbm.c.Addr, gbm.LastBlock)
	// Fetch and return signed blocks since LastBlock
	blocks, err := d.Visor.GetSignedBlocksSince(gbm.LastBlock, gbm.RequestedBlocks)
	if err != nil {
		logger.Infof("Get signed blocks failed: %v", err)
		return
	}

	if len(blocks) == 0 {
		return
	}

	logger.Debugf("Got %d blocks since %d", len(blocks), gbm.LastBlock)

	m := NewGiveBlocksMessage(blocks)
	if err := d.Pool.Pool.SendMessage(gbm.c.Addr, m); err != nil {
		logger.Errorf("Send GiveBlocksMessage to %s failed: %v", gbm.c.Addr, err)
	}
}

// GiveBlocksMessage sent in response to GetBlocksMessage, or unsolicited
type GiveBlocksMessage struct {
	Blocks []coin.SignedBlock
	c      *gnet.MessageContext `enc:"-"`
}

// NewGiveBlocksMessage creates GiveBlocksMessage
func NewGiveBlocksMessage(blocks []coin.SignedBlock) *GiveBlocksMessage {
	return &GiveBlocksMessage{
		Blocks: blocks,
	}
}

// Handle handle message
func (gbm *GiveBlocksMessage) Handle(mc *gnet.MessageContext,
	daemon interface{}) error {
	gbm.c = mc
	return daemon.(*Daemon).recordMessageEvent(gbm, mc)
}

// Process process message
func (gbm *GiveBlocksMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		logger.Critical().Info("Visor disabled, ignoring GiveBlocksMessage")
		return
	}

	processed := 0
	maxSeq := d.Visor.HeadBkSeq()
	for _, b := range gbm.Blocks {
		// To minimize waste when receiving multiple responses from peers
		// we only break out of the loop if the block itself is invalid.
		// E.g. if we request 20 blocks since 0 from 2 peers, and one peer
		// replies with 15 and the other 20, if we did not do this check and
		// the reply with 15 was received first, we would toss the one with 20
		// even though we could process it at the time.
		if b.Seq() <= maxSeq {
			continue
		}

		err := d.Visor.ExecuteSignedBlock(b)
		if err == nil {
			logger.Critical().Infof("Added new block %d", b.Block.Head.BkSeq)
			processed++
		} else {
			logger.Critical().Errorf("Failed to execute received block %d: %v", b.Block.Head.BkSeq, err)
			// Blocks must be received in order, so if one fails its assumed
			// the rest are failing
			break
		}
	}
	if processed == 0 {
		return
	}

	headBkSeq := d.Visor.HeadBkSeq()
	// Announce our new blocks to peers
	m1 := NewAnnounceBlocksMessage(headBkSeq)
	d.Pool.Pool.BroadcastMessage(m1)
	//request more blocks.
	m2 := NewGetBlocksMessage(headBkSeq, d.Visor.Config.BlocksResponseCount)
	d.Pool.Pool.BroadcastMessage(m2)
}

// AnnounceBlocksMessage tells a peer our highest known BkSeq. The receiving peer can choose
// to send GetBlocksMessage in response
type AnnounceBlocksMessage struct {
	MaxBkSeq uint64
	c        *gnet.MessageContext `enc:"-"`
}

// NewAnnounceBlocksMessage creates message
func NewAnnounceBlocksMessage(seq uint64) *AnnounceBlocksMessage {
	return &AnnounceBlocksMessage{
		MaxBkSeq: seq,
	}
}

// Handle handles message
func (abm *AnnounceBlocksMessage) Handle(mc *gnet.MessageContext,
	daemon interface{}) error {
	abm.c = mc
	return daemon.(*Daemon).recordMessageEvent(abm, mc)
}

// Process process message
func (abm *AnnounceBlocksMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}

	headBkSeq := d.Visor.HeadBkSeq()
	if headBkSeq >= abm.MaxBkSeq {
		return
	}

	// TODO: Should this be block get request for current sequence?
	// If client is not caught up, won't attempt to get block
	m := NewGetBlocksMessage(headBkSeq, d.Visor.Config.BlocksResponseCount)
	if err := d.Pool.Pool.SendMessage(abm.c.Addr, m); err != nil {
		logger.Errorf("Send GetBlocksMessage to %s failed: %v", abm.c.Addr, err)
	}
}

// SendingTxnsMessage send transaction message interface
type SendingTxnsMessage interface {
	GetTxns() []cipher.SHA256
}

// AnnounceTxnsMessage tells a peer that we have these transactions
type AnnounceTxnsMessage struct {
	Txns []cipher.SHA256
	c    *gnet.MessageContext `enc:"-"`
}

// NewAnnounceTxnsMessage creates announce txns message
func NewAnnounceTxnsMessage(txns []cipher.SHA256) *AnnounceTxnsMessage {
	return &AnnounceTxnsMessage{
		Txns: txns,
	}
}

// GetTxns returns txns
func (atm *AnnounceTxnsMessage) GetTxns() []cipher.SHA256 {
	return atm.Txns
}

// Handle handle message
func (atm *AnnounceTxnsMessage) Handle(mc *gnet.MessageContext,
	daemon interface{}) error {
	atm.c = mc
	return daemon.(*Daemon).recordMessageEvent(atm, mc)
}

// Process process message
func (atm *AnnounceTxnsMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}

	unknown := d.Visor.UnConfirmFilterKnown(atm.Txns)
	if len(unknown) == 0 {
		return
	}

	m := NewGetTxnsMessage(unknown)
	if err := d.Pool.Pool.SendMessage(atm.c.Addr, m); err != nil {
		logger.Errorf("Send GetTxnsMessage to %s failed: %v", atm.c.Addr, err)
	}
}

// GetTxnsMessage request transactions of given hash
type GetTxnsMessage struct {
	Txns []cipher.SHA256
	c    *gnet.MessageContext `enc:"-"`
}

// NewGetTxnsMessage creates GetTxnsMessage
func NewGetTxnsMessage(txns []cipher.SHA256) *GetTxnsMessage {
	return &GetTxnsMessage{
		Txns: txns,
	}
}

// Handle handle message
func (gtm *GetTxnsMessage) Handle(mc *gnet.MessageContext, daemon interface{}) error {
	gtm.c = mc
	return daemon.(*Daemon).recordMessageEvent(gtm, mc)
}

// Process process message
func (gtm *GetTxnsMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}

	// Locate all txns from the unconfirmed pool
	known := d.Visor.UnConfirmKnow(gtm.Txns)
	if len(known) == 0 {
		return
	}

	// Reply to sender with GiveTxnsMessage
	m := NewGiveTxnsMessage(known)
	if err := d.Pool.Pool.SendMessage(gtm.c.Addr, m); err != nil {
		logger.Errorf("Send GiveTxnsMessage to %s failed: %v", gtm.c.Addr, err)
	}
}

// GiveTxnsMessage tells the transaction of given hashes
type GiveTxnsMessage struct {
	Txns coin.Transactions
	c    *gnet.MessageContext `enc:"-"`
}

// NewGiveTxnsMessage creates GiveTxnsMessage
func NewGiveTxnsMessage(txns coin.Transactions) *GiveTxnsMessage {
	return &GiveTxnsMessage{
		Txns: txns,
	}
}

// GetTxns returns transactions hashes
func (gtm *GiveTxnsMessage) GetTxns() []cipher.SHA256 {
	return gtm.Txns.Hashes()
}

// Handle handle message
func (gtm *GiveTxnsMessage) Handle(mc *gnet.MessageContext,
	daemon interface{}) error {
	gtm.c = mc
	return daemon.(*Daemon).recordMessageEvent(gtm, mc)
}

// Process process message
func (gtm *GiveTxnsMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}

	hashes := make([]cipher.SHA256, 0, len(gtm.Txns))
	// Update unconfirmed pool with these transactions
	for _, txn := range gtm.Txns {
		// Only announce transactions that are new to us, so that peers can't spam relays
		known, softErr, err := d.Visor.InjectTransaction(txn)
		if err != nil {
			logger.Warningf("Failed to record transaction %s: %v", txn.Hash().Hex(), err)
			continue
		} else if softErr != nil {
			logger.Warningf("Transaction soft violation: %v", err)
			continue
		} else if known {
			logger.Warningf("Duplicate Transaction: %s", txn.Hash().Hex())
			continue
		}

		hashes = append(hashes, txn.Hash())
	}

	// Announce these transactions to peers
	if len(hashes) != 0 {
		logger.Debugf("Announce %d transactions", len(hashes))
		m := NewAnnounceTxnsMessage(hashes)
		d.Pool.Pool.BroadcastMessage(m)
	}
}

// GetTrustMessage request transactions of given hash
type GetTrustMessage struct {
	c *gnet.MessageContext `enc:"-"`
}

// NewGetTrustMessage creates GetTrustMessage
func NewGetTrustMessage() *GetTrustMessage {
	return &GetTrustMessage{}
}

// Handle handle message
func (gtm *GetTrustMessage) Handle(mc *gnet.MessageContext, daemon interface{}) error {
	gtm.c = mc
	return daemon.(*Daemon).recordMessageEvent(gtm, mc)
}

// Process process message
func (gtm *GetTrustMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}

	// Reply to sender with GiveTrustMessage
	if d.Visor.v.IsGenesisNode() {
		// Locate all txns from the unconfirmed pool
		trustNodes := d.Visor.TrustNodes()
		m := NewGiveTrustMessage(trustNodes, d.Visor.v.Config.BlockchainSeckey)
		if err := d.Pool.Pool.SendMessage(gtm.c.Addr, m); err != nil {
			logger.Errorf("Send GiveTrustMessage to %s failed: %v", gtm.c.Addr, err)
		}
	}
}

// GiveTrustMessage tells the transaction of given hashes
type GiveTrustMessage struct {
	Trust []cipher.PubKey
	Sig   cipher.Sig
	c     *gnet.MessageContext `enc:"-"`
}

func pubkeysArrHash(pubkeys []cipher.PubKey) cipher.SHA256 {
	trustKey := []byte{}
	for _, pk := range pubkeys {
		trustKey = append(trustKey, pk[:]...)
	}
	return cipher.SumSHA256(trustKey)
}

// NewGiveTrustMessage creates GiveTrustMessage
func NewGiveTrustMessage(trust []cipher.PubKey, secKey cipher.SecKey) *GiveTrustMessage {
	trustHash := pubkeysArrHash(trust)
	sig := cipher.SignHash(trustHash, secKey)
	return &GiveTrustMessage{
		Trust: trust,
		Sig:   sig,
	}
}

// GetTrust returns transactions hashes
func (gtm *GiveTrustMessage) GetTrust() []cipher.PubKey {
	return gtm.Trust
}

// Handle handle message
func (gtm *GiveTrustMessage) Handle(mc *gnet.MessageContext,
	daemon interface{}) error {
	gtm.c = mc
	return daemon.(*Daemon).recordMessageEvent(gtm, mc)
}

// Process process message
func (gtm *GiveTrustMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}

	// Announce these transactions to peers
	if len(gtm.Trust) != 0 {
		// verify signature
		verifiedHash := pubkeysArrHash(gtm.Trust)
		err := cipher.VerifySignature(d.Visor.v.Config.BlockchainPubkey, gtm.Sig, verifiedHash)
		if err != nil {
			return
		}
		if err := d.Visor.v.InsertTrustPubkeyList(gtm.Trust); err != nil {
			return
		}
		logger.Debugf("Announce %d trust message", len(gtm.Trust))
		m := NewAnnounceTrustMessage(gtm.Trust)
		d.Pool.Pool.BroadcastMessage(m)
	}
}

// AnnounceTrustMessage tells a peer that we have these transactions
type AnnounceTrustMessage struct {
	Trust []cipher.PubKey
	c     *gnet.MessageContext `enc:"-"`
}

// NewAnnounceTrustMessage creates announce trust message
func NewAnnounceTrustMessage(trust []cipher.PubKey) *AnnounceTrustMessage {
	return &AnnounceTrustMessage{
		Trust: trust,
	}
}

// GetTrust returns trust
func (atm *AnnounceTrustMessage) GetTrust() []cipher.PubKey {
	return atm.Trust
}

// Handle handle message
func (atm *AnnounceTrustMessage) Handle(mc *gnet.MessageContext,
	daemon interface{}) error {
	atm.c = mc
	return daemon.(*Daemon).recordMessageEvent(atm, mc)
}

// Process process message
func (atm *AnnounceTrustMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}

	m := NewGetTrustMessage()
	if err := d.Pool.Pool.SendMessage(atm.c.Addr, m); err != nil {
		logger.Errorf("Send GetTrustMessage to %s failed: %v", atm.c.Addr, err)
	}
}

// GetPrepareMessage request transactions of given hash
type GetPrepareMessage struct {
	Hash cipher.SHA256
	c    *gnet.MessageContext `enc:"-"`
}

// NewGetPrepareMessage creates GetPrepareMessage
func NewGetPrepareMessage(hash cipher.SHA256) *GetPrepareMessage {
	return &GetPrepareMessage{Hash: hash}
}

// Handle handle message
func (gpm *GetPrepareMessage) Handle(mc *gnet.MessageContext, daemon interface{}) error {
	gpm.c = mc
	return daemon.(*Daemon).recordMessageEvent(gpm, mc)
}

// Process process message
func (gpm *GetPrepareMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}

	// Reply to sender with GivePrepareMessage
	if d.Visor.v.Config.IsMaster {
		// todo: should check hash exists or not
		if d.Visor.v.CheckHashExists(gpm.Hash) || d.Visor.v.CheckHashExistsInChain(gpm.Hash) {
			m := NewGivePrepareMessage(gpm.Hash, d.Visor.v.Config.BlockchainTrustSeckey)
			if err := d.Pool.Pool.SendMessage(gpm.c.Addr, m); err != nil {
				logger.Errorf("Send GivePrepareMessage to %s failed: %v", gpm.c.Addr, err)
			}
		}
	}
}

// GivePrepareMessage tells the transaction of given hashes
type GivePrepareMessage struct {
	Hash cipher.SHA256
	Sig  cipher.Sig
	c    *gnet.MessageContext `enc:"-"`
}

// NewGivePrepareMessage creates GivePrepareMessage
func NewGivePrepareMessage(hash cipher.SHA256, secKey cipher.SecKey) *GivePrepareMessage {
	sig := cipher.SignHash(hash, secKey)
	return &GivePrepareMessage{
		Hash: hash,
		Sig:  sig,
	}
}

// Handle handle message
func (gpm *GivePrepareMessage) Handle(mc *gnet.MessageContext,
	daemon interface{}) error {
	gpm.c = mc
	return daemon.(*Daemon).recordMessageEvent(gpm, mc)
}

// Process process message
func (gpm *GivePrepareMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}

	// verify signature
	pubkeyRec, err := cipher.PubKeyFromSig(gpm.Sig, gpm.Hash) //recovered pubkey
	if err != nil {
		logger.Errorf("Invalid sig: PubKey recovery failed: %v", err)
		return
	}
	if d.Visor.v.IsTrustPubkey(pubkeyRec) {
		pubkeys, err := d.Visor.v.GetBlockValidators(gpm.Hash)
		if err != nil {
			logger.Errorf("Get block %s validator failed, waiting pending block added", gpm.Hash.Hex())
			return
		}
		err = d.Visor.v.AddValidator(gpm.Hash, pubkeyRec)
		if err != nil {
			logger.Errorf("AddValidator %s for hash failed: %v", pubkeyRec.Hex(), err)
		}
		for _, v := range pubkeys {
			logger.Debugf("pubkey %s", v.Hex())
		}
		err = CanMakeBlock(d, gpm.Hash)
		if err != nil {
			return
		}

		sb, err := d.Visor.v.GetBlockByHash(gpm.Hash)
		if err != nil {
			logger.Errorf("get block by hash %s failed", gpm.Hash.Hex())
			return
		}
		err = d.Visor.broadcastBlock(*sb, d.Pool)
		if err != nil {
			logger.Errorf("broadcast block %s failed", sb.HashHeader())
			return
		}
	}

	if d.Visor.v.Config.IsMaster {
		// todo handle prepare msg
		m := NewAnnouncePrepareMessage(gpm.Hash, d.Visor.v.Config.BlockchainTrustSeckey)
		d.Pool.Pool.BroadcastMessage(m)
	}
}

// AnnouncePrepareMessage tells a peer that we have these transactions
type AnnouncePrepareMessage struct {
	Hash cipher.SHA256
	Sig  cipher.Sig
	c    *gnet.MessageContext `enc:"-"`
}

// NewAnnouncePrepareMessage creates announce trust message
func NewAnnouncePrepareMessage(hash cipher.SHA256, secKey cipher.SecKey) *AnnouncePrepareMessage {
	sig := cipher.SignHash(hash, secKey)
	return &AnnouncePrepareMessage{
		Hash: hash,
		Sig:  sig,
	}
}

// Handle handle message
func (apm *AnnouncePrepareMessage) Handle(mc *gnet.MessageContext,
	daemon interface{}) error {
	apm.c = mc
	return daemon.(*Daemon).recordMessageEvent(apm, mc)
}

// Process process message
func (apm *AnnouncePrepareMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}
	pubkeyRec, err := cipher.PubKeyFromSig(apm.Sig, apm.Hash) //recovered pubkey
	if err != nil {
		logger.Errorf("Invalid sig: PubKey recovery failed: %v", err)
		return
	}
	// check hash should request or not
	if d.Visor.v.CheckHashExists(apm.Hash) {
		//todo the logic is stupid, pubkey not exists
		if !d.Visor.v.CheckPubkeyExists(apm.Hash, pubkeyRec) {
			m := NewGetPrepareMessage(apm.Hash)
			if err := d.Pool.Pool.SendMessage(apm.c.Addr, m); err != nil {
				logger.Errorf("Send GetPrepareMessage to %s failed: %v", apm.c.Addr, err)
			}
		}
	}
}

// RequestPrepare Sends a GetPrepareMessage to all connections
func (vs *Visor) RequestPrepare(pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	err := vs.strand("RequestPrepare", func() error {
		for _, hash := range vs.v.GetPendingHash() {
			m := NewGetPrepareMessage(hash)
			err := pool.Pool.BroadcastMessage(m)
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		logger.Debugf("Broadcast GetPrepareMessage failed: %v", err)
	}

	return err
}

// RequestAgreeNodeNum Sends a GetAgreeNumMessage to all connections
func (vs *Visor) RequestAgreeNodeNum(pool *Pool) error {
	if vs.Config.DisableNetworking {
		return nil
	}

	err := vs.strand("RequestAgressNodeNum", func() error {
		m := NewGetAgreeNumMessage()
		return pool.Pool.BroadcastMessage(m)
	})

	if err != nil {
		logger.Debugf("Broadcast GetAgreeNumMessage failed: %v", err)
	}

	return err
}

// GetAgreeNumMessage request transactions of given hash
type GetAgreeNumMessage struct {
	c *gnet.MessageContext `enc:"-"`
}

// NewGetAgreeNumMessage creates GetAgreeNumMessage
func NewGetAgreeNumMessage() *GetAgreeNumMessage {
	return &GetAgreeNumMessage{}
}

// Handle handle message
func (gtm *GetAgreeNumMessage) Handle(mc *gnet.MessageContext, daemon interface{}) error {
	gtm.c = mc
	return daemon.(*Daemon).recordMessageEvent(gtm, mc)
}

// Process process message
func (gtm *GetAgreeNumMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}

	// Reply to sender with GiveAgreeNumMessage
	if d.Visor.v.IsGenesisNode() {
		// Locate all txns from the unconfirmed pool
		agreeNumNodes := d.Visor.v.GetAgreeNodeNum()
		m := NewGiveAgreeNumMessage(agreeNumNodes, d.Visor.v.Config.BlockchainSeckey)
		if err := d.Pool.Pool.SendMessage(gtm.c.Addr, m); err != nil {
			logger.Errorf("Send GiveAgreeNumMessage to %s failed: %v", gtm.c.Addr, err)
		}
	}
}

// GiveAgreeNumMessage tells the transaction of given hashes
type GiveAgreeNumMessage struct {
	AgreeNum string
	Sig      cipher.Sig
	c        *gnet.MessageContext `enc:"-"`
}

func intHash(num int) cipher.SHA256 {
	return cipher.SumSHA256([]byte(strconv.Itoa(num)))
}

// NewGiveAgreeNumMessage creates GiveAgreeNumMessage
func NewGiveAgreeNumMessage(agreeNum int, secKey cipher.SecKey) *GiveAgreeNumMessage {
	sig := cipher.SignHash(intHash(agreeNum), secKey)
	return &GiveAgreeNumMessage{
		AgreeNum: strconv.Itoa(agreeNum),
		Sig:      sig,
	}
}

// Handle handle message
func (gtm *GiveAgreeNumMessage) Handle(mc *gnet.MessageContext,
	daemon interface{}) error {
	gtm.c = mc
	return daemon.(*Daemon).recordMessageEvent(gtm, mc)
}

// Process process message
func (gtm *GiveAgreeNumMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		return
	}

	num, err := strconv.Atoi(gtm.AgreeNum)
	if err != nil {
		return
	}
	if num > 0 {
		// verify signature
		verifiedHash := intHash(num)
		err := cipher.VerifySignature(d.Visor.v.Config.BlockchainPubkey, gtm.Sig, verifiedHash)
		if err != nil {
			return
		}
		if err := d.Visor.v.InsertAgreeNodeNum(num); err != nil {
			return
		}
	}
}

// GivePendingBlockMessage sent in response to GetBlocksMessage, or unsolicited
type GivePendingBlockMessage struct {
	PendingBlock []coin.PendingSignedBlock
	c            *gnet.MessageContext `enc:"-"`
}

// NewGivePendingBlockMessage creates GivePendingBlockMessage
func NewGivePendingBlockMessage(blocks []coin.PendingSignedBlock) *GivePendingBlockMessage {
	return &GivePendingBlockMessage{
		PendingBlock: blocks,
	}
}

// Handle handle message
func (gbm *GivePendingBlockMessage) Handle(mc *gnet.MessageContext,
	daemon interface{}) error {
	gbm.c = mc
	return daemon.(*Daemon).recordMessageEvent(gbm, mc)
}

// Process process message
func (gbm *GivePendingBlockMessage) Process(d *Daemon) {
	if d.Visor.Config.DisableNetworking {
		logger.Critical().Info("Visor disabled, ignoring GivePendingBlockMessage")
		return
	}

	if !d.Visor.v.Config.IsMaster {
		return
	}

	maxSeq := d.Visor.HeadBkSeq()
	for _, b := range gbm.PendingBlock {
		if b.Seq() <= maxSeq {
			continue
		}

		if b.Pending {
			err := d.Visor.AddPendingBlock(b.ToSignedBlock())
			if err == nil {
				logger.Critical().Infof("Added pending block %d", b.Block.Head.BkSeq)
				if d.Visor.v.Config.IsMaster {
					err := d.Visor.v.AddValidator(b.HashHeader(), d.Visor.v.Config.BlockchainTrustPubkey)
					if err != nil {
						logger.Critical().Infof("AddValidator failed %v", err)
					}
					m := NewGivePrepareMessage(b.HashHeader(), d.Visor.v.Config.BlockchainTrustSeckey)
					d.Pool.Pool.BroadcastMessage(m)
				}
			} else {
				logger.Critical().Errorf("Failed to add pending block %d: %v", b.Block.Head.BkSeq, err)
				break
			}
		}
	}

}
