package pbft

import (
	"errors"
	"fmt"
	"sync"

	"github.com/samoslab/samos/src/cipher"
	"github.com/samoslab/samos/src/coin"
	"github.com/samoslab/samos/src/util/utc"
)

// PBFT pending block data
type PBFT struct {
	Status        int
	BlockNum      int
	PendingBlocks map[cipher.SHA256]coin.SignedBlock
	PreparedInfos map[cipher.SHA256][]cipher.PubKey
	BlockTime     map[cipher.SHA256]int64
	mutex         sync.Mutex
}

// NewPBFT new pbft
func NewPBFT() *PBFT {
	return &PBFT{
		Status:        0,
		BlockNum:      0,
		PendingBlocks: make(map[cipher.SHA256]coin.SignedBlock, 1),
		BlockTime:     make(map[cipher.SHA256]int64, 1),
		PreparedInfos: make(map[cipher.SHA256][]cipher.PubKey, 1),
	}
}

// RemoveUnconfirmBlock remove pending block if it unconfirmed in 120 seconds
func (p *PBFT) RemoveUnconfirmBlock() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	now := utc.UnixNow()
	for hash := range p.PendingBlocks {
		createdTime, _ := p.BlockTime[hash]
		if now-createdTime > 120 {
			fmt.Printf("delete block hash %s because it is not confirmed in 120s\n", hash.Hex())
			delete(p.PendingBlocks, hash)
			delete(p.BlockTime, hash)
			delete(p.PreparedInfos, hash)
			p.BlockNum--
		}
	}
}

// GetSignedBlock get SignedBlock for the hash
func (p *PBFT) GetSignedBlock(hash cipher.SHA256) (coin.SignedBlock, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if sb, ok := p.PendingBlocks[hash]; ok {
		return sb, nil
	}
	return coin.SignedBlock{}, errors.New("block not exists")
}

// CheckBkSeq check seq validaty
func (p *PBFT) CheckBkSeq(seq uint64) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, block := range p.PendingBlocks {
		if seq <= block.Seq() {
			return errors.New("pending blockseq less than pbft")
		}
	}
	return nil
}

// DeleteHash delete hash from pending block map
func (p *PBFT) DeleteHash(hash cipher.SHA256) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if _, ok := p.PendingBlocks[hash]; !ok {
		return errors.New("block hash not exists")
	}

	delete(p.PendingBlocks, hash)
	delete(p.PreparedInfos, hash)
	delete(p.BlockTime, hash)
	p.BlockNum--

	return nil
}

// WaitingConfirmedBlockHash block hash that waiting other validator message
func (p *PBFT) WaitingConfirmedBlockHash() []cipher.SHA256 {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	watingHash := []cipher.SHA256{}
	for hash := range p.PendingBlocks {
		watingHash = append(watingHash, hash)
	}
	return watingHash
}

// AddSignedBlock add a signed block
func (p *PBFT) AddSignedBlock(sb coin.SignedBlock) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	bh := sb.Block.HashHeader()
	if _, ok := p.PendingBlocks[bh]; ok {
		return errors.New("the block has added")
	}

	if p.BlockNum >= 1 {
		return errors.New("has unconfirmed block, this block cannot added")
	}
	pubkeyRec, err := cipher.PubKeyFromSig(sb.Sig, bh) //recovered pubkey
	if err != nil {
		return errors.New("Invalid sig: PubKey recovery failed")
	}
	p.PendingBlocks[bh] = sb
	p.PreparedInfos[bh] = []cipher.PubKey{pubkeyRec}
	p.BlockTime[bh] = utc.UnixNow()
	p.BlockNum++
	return nil
}

// GetBlockValidators returns all pubkeys for block hash
func (p *PBFT) GetBlockValidators(hash cipher.SHA256) ([]cipher.PubKey, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	validators, ok := p.PreparedInfos[hash]
	if !ok {
		return []cipher.PubKey{}, errors.New("not exists")
	}
	return validators, nil
}

// CheckPubkeyExists check pubkey exists for the block hash
func (p *PBFT) CheckPubkeyExists(hash cipher.SHA256, pubkey cipher.PubKey) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	validators, ok := p.PreparedInfos[hash]
	if !ok {
		return errors.New("not exists")
	}
	for _, pk := range validators {
		if pk == pubkey {
			return nil
		}
	}
	return errors.New("not exists")
}

// AddValidator add a validator , if validator number exceed threshold, then make block into chain
func (p *PBFT) AddValidator(hash cipher.SHA256, pubkey cipher.PubKey) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	validators, ok := p.PreparedInfos[hash]
	if !ok {
		return errors.New("this block hash not added into prepared infos")
	}
	for _, pk := range validators {
		if pk == pubkey {
			return errors.New("the pubkey already exists")
		}
	}
	validators = append(validators, pubkey)
	p.PreparedInfos[hash] = validators
	return nil
}

// ValidatorNumber the nunber of validator for the block hash
func (p *PBFT) ValidatorNumber(hash cipher.SHA256) (int, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	validators, ok := p.PreparedInfos[hash]
	if !ok {
		return 0, errors.New("the hash not exists")
	}
	return len(validators), nil
}
