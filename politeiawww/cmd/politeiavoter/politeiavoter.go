// Copyright (c) 2018-2021 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/decred/dcrd/dcrutil/v3"
	"io"
	"math"
	"math/big"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "decred.org/dcrwallet/rpc/walletrpc"
	"github.com/decred/dcrd/blockchain/stake/v3"
	"github.com/decred/dcrd/chaincfg/chainhash"
	"github.com/decred/dcrd/wire"
	"github.com/decred/politeia/politeiad/api/v1/identity"
	piv1 "github.com/decred/politeia/politeiawww/api/pi/v1"
	rcv1 "github.com/decred/politeia/politeiawww/api/records/v1"
	tkv1 "github.com/decred/politeia/politeiawww/api/ticketvote/v1"
	v1 "github.com/decred/politeia/politeiawww/api/www/v1"
	"github.com/decred/politeia/politeiawww/client"
	"github.com/decred/politeia/util"
	"github.com/gorilla/schema"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/net/publicsuffix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	cmdInventory  = "inventory"
	cmdStats      = "stats"
	cmdVote       = "vote"
	cmdTally      = "tally"
	cmdTallyTable = "tally-table"
	cmdVerify     = "verify"
	cmdHelp       = "help"
)

const (
	failedJournal  = "failed.json"
	successJournal = "success.json"
	workJournal    = "work.json"
)

const (
	voteModeMirror  = "mirror"
	voteModeNumber  = "number"
	voteModePercent = "percent"
)

func generateSeed() (int64, error) {
	var seedBytes [8]byte
	_, err := crand.Read(seedBytes[:])
	if err != nil {
		return 0, err
	}
	return new(big.Int).SetBytes(seedBytes[:]).Int64(), nil
}

// walletPassphrase returns the wallet passphrase from the config if one was
// provided or prompts the user for their wallet passphrase if one was not
// provided.
func (p *piv) walletPassphrase() ([]byte, error) {
	if p.cfg.WalletPassphrase != "" {
		return []byte(p.cfg.WalletPassphrase), nil
	}

	prompt := "Enter the private passphrase of your wallet: "
	for {
		fmt.Print(prompt)
		pass, err := terminal.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return nil, err
		}
		fmt.Print("\n")
		pass = bytes.TrimSpace(pass)
		if len(pass) == 0 {
			continue
		}

		return pass, nil
	}
}

// piv is the client context.
type piv struct {
	sync.RWMutex                       // retryQ lock
	ballotResults []tkv1.CastVoteReply // results of voting
	votedYes      int
	votedNo       int

	run time.Time // when this run started

	cfg *config // application config

	// https
	client    *http.Client
	id        *identity.PublicIdentity
	userAgent string

	// wallet grpc
	ctx    context.Context
	cancel context.CancelFunc
	creds  credentials.TransportCredentials
	conn   *grpc.ClientConn
	wallet pb.WalletServiceClient
	cache  *piCache

	version   *v1.VersionReply
	summaries map[string]tkv1.Summary
	mux       sync.RWMutex
}

func newPiVoter(shutdownCtx context.Context, cfg *config) (*piv, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.SkipVerify,
	}
	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
		Dial:            cfg.dial,
	}
	if cfg.Proxy != "" {
		tr.MaxConnsPerHost = 1
		tr.DisableKeepAlives = true
	}
	jar, err := cookiejar.New(&cookiejar.Options{
		PublicSuffixList: publicsuffix.List,
	})
	if err != nil {
		return nil, err
	}

	// Wallet GRPC
	serverCAs := x509.NewCertPool()
	serverCert, err := os.ReadFile(cfg.WalletCert)
	if err != nil {
		return nil, err
	}
	if !serverCAs.AppendCertsFromPEM(serverCert) {
		return nil, fmt.Errorf("no certificates found in %s",
			cfg.WalletCert)
	}
	keypair, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
	if err != nil {
		return nil, fmt.Errorf("read client keypair: %v", err)
	}
	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{keypair},
		RootCAs:      serverCAs,
	})

	conn, err := grpc.Dial(cfg.WalletHost,
		grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, err
	}
	wallet := pb.NewWalletServiceClient(conn)
	cache, err := newCache(cfg.CachePath, time.Duration(cfg.CacheTimeout)*time.Hour)
	if err != nil {
		return nil, err
	}
	if cfg.CacheClear {
		go cache.Clear()
	}
	// return context
	return &piv{
		run:    time.Now(),
		ctx:    shutdownCtx,
		creds:  creds,
		conn:   conn,
		wallet: wallet,
		cfg:    cfg,
		client: &http.Client{
			Transport: tr,
			Jar:       jar,
		},
		userAgent: fmt.Sprintf("politeiavoter/%s", cfg.Version),
		cache:     cache,
		summaries: make(map[string]tkv1.Summary),
	}, nil
}

type JSONTime struct {
	Time string `json:"time"`
}

func (p *piv) jsonLog(filename, token string, work ...interface{}) error {
	dir := filepath.Join(p.cfg.voteDir, token)
	os.MkdirAll(dir, 0700)

	p.Lock()
	defer p.Unlock()

	f := filepath.Join(dir, fmt.Sprintf("%v.%v", filename, p.run.Unix()))
	fh, err := os.OpenFile(f, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer fh.Close()

	e := json.NewEncoder(fh)
	e.SetIndent("", "  ")
	err = e.Encode(JSONTime{
		Time: time.Now().Format(time.StampNano),
	})
	if err != nil {
		return err
	}
	for _, v := range work {
		err = e.Encode(v)
		if err != nil {
			return err
		}
	}

	return nil
}

func convertTicketHashes(h []string) ([][]byte, error) {
	hashes := make([][]byte, 0, len(h))
	for _, v := range h {
		hh, err := chainhash.NewHashFromStr(v)
		if err != nil {
			return nil, err
		}
		hashes = append(hashes, hh[:])
	}
	return hashes, nil
}

func (p *piv) testMaybeFail(b interface{}) ([]byte, error) {
	switch p.cfg.testingMode {
	case testFailUnrecoverable:
		return nil, fmt.Errorf("%v, %v %v", http.StatusBadRequest,
			255, "fake")
	default:
	}
	// Fail every 3rd vote
	p.Lock()
	p.cfg.testingCounter++
	if p.cfg.testingCounter%3 == 0 {
		p.Unlock()
		return nil, ErrRetry{
			At:   "FAKE r.StatusCode != http.StatusOK",
			Err:  fmt.Errorf("fake error"),
			Body: []byte{},
			Code: http.StatusRequestTimeout,
		}
	}
	p.Unlock()

	// Fake out CastBallotReply. We cast b to CastBallot but this
	// may have to change in the future if we add additional
	// functionality here.
	cbr := tkv1.CastBallotReply{
		Receipts: []tkv1.CastVoteReply{
			{
				Ticket:  b.(*tkv1.CastBallot).Votes[0].Ticket,
				Receipt: "receipt",
				//ErrorCode:    tkv1.VoteErrorInternalError,
				//ErrorContext: "testing",
			},
		},
	}
	jcbr, err := json.Marshal(cbr)
	if err != nil {
		return nil, fmt.Errorf("TEST FAILED: %v", err)
	}
	return jcbr, nil
}

func (p *piv) makeRequest(method, api, route string, b interface{}) ([]byte, error) {
	var requestBody []byte
	var queryParams string
	var startTime = time.Now()
	if b != nil {
		if method == http.MethodGet {
			// GET requests don't have a request body; instead we will populate
			// the query params.
			form := url.Values{}
			err := schema.NewEncoder().Encode(b, form)
			if err != nil {
				return nil, err
			}

			queryParams = "?" + form.Encode()
		} else {
			var err error
			requestBody, err = json.Marshal(b)
			if err != nil {
				return nil, err
			}
		}
	}

	fullRoute := p.cfg.PoliteiaWWW + api + route + queryParams
	log.Debugf("Request: %v %v", method, fullRoute)
	if len(requestBody) != 0 {
		log.Tracef("%v  ", string(requestBody))
	}

	// This is a hack to test this code.
	if p.cfg.testing {
		return p.testMaybeFail(b)
	}
	req, err := http.NewRequestWithContext(p.ctx, method, fullRoute,
		bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", p.userAgent)
	r, err := p.client.Do(req)
	if err != nil {
		return nil, ErrRetry{
			At:  "p.client.Do(req)",
			Err: err,
		}
	}
	defer func() {
		r.Body.Close()
	}()
	fmt.Printf("%s[%s] request took %s. Status code[%d]\n", method, api+route, time.Since(startTime), r.StatusCode)
	responseBody := util.ConvertBodyToByteArray(r.Body, false)
	log.Tracef("Response: %v %v", r.StatusCode, string(responseBody))

	switch r.StatusCode {
	case http.StatusOK:
		// Nothing to do. Continue.
	case http.StatusBadRequest:
		// The error was caused by the client. These will result in
		// the same error every time so should not be retried.
		var ue tkv1.UserErrorReply
		err = json.Unmarshal(responseBody, &ue)
		if err == nil && ue.ErrorCode != 0 {
			return nil, fmt.Errorf("%v, %v %v", r.StatusCode,
				tkv1.ErrorCodes[ue.ErrorCode], ue.ErrorContext)
		}
	default:
		// Retry all other errors
		return nil, ErrRetry{
			At:   "r.StatusCode != http.StatusOK",
			Err:  err,
			Body: responseBody,
			Code: r.StatusCode,
		}
	}

	return responseBody, nil
}

// getVersion returns the server side version structure.
func (p *piv) getVersion() (*v1.VersionReply, error) {
	if p.version != nil {
		return p.version, nil
	}
	responseBody, err := p.makeRequest(http.MethodGet,
		v1.PoliteiaWWWAPIRoute, v1.RouteVersion, nil)
	if err != nil {
		return nil, err
	}

	var v v1.VersionReply
	err = json.Unmarshal(responseBody, &v)
	if err != nil {
		return nil, fmt.Errorf("Could not unmarshal version: %v", err)
	}
	p.version = &v
	return &v, nil
}

// firstContact connect to the wallet and it obtains the version structure from
// the politeia server.
func firstContact(shutdownCtx context.Context, cfg *config) (*piv, error) {
	// Always hit / first for to obtain the server identity and api version
	p, err := newPiVoter(shutdownCtx, cfg)
	if err != nil {
		return nil, err
	}
	version, err := p.getVersion()
	if err != nil {
		return nil, err
	}
	log.Debugf("Version: %v", version.Version)
	log.Debugf("Route  : %v", version.Route)
	log.Debugf("Pubkey : %v", version.PubKey)

	p.id, err = identity.PublicIdentityFromString(version.PubKey)
	if err != nil {
		return nil, err
	}

	return p, nil
}

// eligibleVotes takes a vote result reply that contains the full list of the
// votes already cast along with a committed tickets response from wallet which
// consists of a list of tickets the wallet is aware of and returns a list of
// tickets that the wallet is actually able to sign and vote with.
//
// When a ticket has already voted, the signature is also checked to ensure it
// is valid.  In the case it is invalid, and the wallet can sign it, the ticket
// is included so it may be resubmitted.  This could be caused by bad data on
// the server or if the server is lying to the client.
func (p *piv) eligibleVotes(rr *tkv1.ResultsReply, ctres *pb.CommittedTicketsResponse) (votedYes, votedNo, eligible []*pb.CommittedTicketsResponse_TicketAddress, err error) {
	// Put cast votes into a map to filter in linear time
	castVotes := make(map[string]tkv1.CastVoteDetails)
	for _, v := range rr.Votes {
		castVotes[v.Ticket] = v
	}

	// Filter out tickets that have already voted. If a ticket has
	// voted but the signature is invalid, resubmit the vote. This
	// could be caused by bad data on the server or if the server is
	// lying to the client.
	eligible = make([]*pb.CommittedTicketsResponse_TicketAddress, 0,
		len(ctres.TicketAddresses))
	for _, t := range ctres.TicketAddresses {
		h, err := chainhash.NewHash(t.Ticket)
		if err != nil {
			return nil, nil, nil, err
		}

		// Filter out tickets tracked by imported xpub accounts.
		r, err := p.wallet.GetTransaction(context.TODO(), &pb.GetTransactionRequest{
			TransactionHash: h[:],
		})
		if err != nil {
			log.Error(err)
			continue
		}
		tx := new(wire.MsgTx)
		err = tx.Deserialize(bytes.NewReader(r.Transaction.Transaction))
		if err != nil {
			log.Error(err)
			continue
		}
		addr, err := stake.AddrFromSStxPkScrCommitment(tx.TxOut[1].PkScript, activeNetParams.Params)
		if err != nil {
			log.Error(err)
			continue
		}
		vr, err := p.wallet.ValidateAddress(context.TODO(), &pb.ValidateAddressRequest{
			Address: addr.String(),
		})
		if err != nil {
			log.Error(err)
			continue
		}
		if vr.AccountNumber >= 1<<31-1 { // imported xpub account
			// do not append to filtered.
			continue
		}

		detail, ok := castVotes[h.String()]
		if !ok {
			eligible = append(eligible, t)
		} else {
			if detail.VoteBit == VoteBitYes {
				votedYes = append(votedYes, t)
			} else {
				votedNo = append(votedNo, t)
			}
		}
	}

	return votedYes, votedNo, eligible, nil
}

func (p *piv) statsVotes(rr *tkv1.ResultsReply, ctres *pb.CommittedTicketsResponse) (me, them *VoteStats, err error) {
	// Put cast votes into a map to filter in linear time
	castVotes := make(map[string]tkv1.CastVoteDetails)
	for _, v := range rr.Votes {
		castVotes[v.Ticket] = v
	}

	me = &VoteStats{}
	them = &VoteStats{}
	for _, t := range ctres.TicketAddresses {
		h, err := chainhash.NewHash(t.Ticket)
		if err != nil {
			return nil, nil, err
		}
		mine := true
		tx := new(wire.MsgTx)
		var addr dcrutil.Address
		var vr *pb.ValidateAddressResponse
		// Filter out tickets tracked by imported xpub accounts.
		r, err := p.wallet.GetTransaction(context.TODO(), &pb.GetTransactionRequest{
			TransactionHash: h[:],
		})
		if err != nil {
			mine = false
			goto result
		}

		err = tx.Deserialize(bytes.NewReader(r.Transaction.Transaction))
		if err != nil {
			mine = false
			goto result
		}
		addr, err = stake.AddrFromSStxPkScrCommitment(tx.TxOut[1].PkScript, activeNetParams.Params)
		if err != nil {
			mine = false
			goto result
		}
		vr, err = p.wallet.ValidateAddress(context.TODO(), &pb.ValidateAddressRequest{
			Address: addr.String(),
		})
		if err != nil {
			mine = false
			goto result
		}
		if vr.AccountNumber >= 1<<31-1 { // imported xpub account
			mine = false
			goto result
		}
	result:
		detail, ok := castVotes[h.String()]
		var owner *VoteStats
		if mine {
			owner = me
		} else {
			owner = them
		}
		if !ok {
			owner.Yet++
		} else {
			if detail.VoteBit == VoteBitYes {
				owner.Yes++
			} else {
				owner.No++
			}
		}
	}

	return me, them, nil
}

func (p *piv) _inventory(i tkv1.Inventory) (*tkv1.InventoryReply, error) {
	responseBody, err := p.makeRequest(http.MethodPost,
		tkv1.APIRoute, tkv1.RouteInventory, i)
	if err != nil {
		return nil, err
	}

	var ar tkv1.InventoryReply
	err = json.Unmarshal(responseBody, &ar)
	if err != nil {
		return nil, fmt.Errorf("Could not unmarshal InventoryReply: %v",
			err)
	}

	return &ar, nil
}

// voteDetails sends a ticketvote API Details request, then verifies and
// returns the reply.
func (p *piv) voteDetails(token, serverPubKey string) (*tkv1.DetailsReply, error) {
	var cacheKey = http.MethodPost + tkv1.APIRoute + tkv1.RouteDetails + token
	responseBody, err := p.cache.Get(cacheKey)
	if err != nil {
		d := tkv1.Details{
			Token: token,
		}
		responseBody, err = p.makeRequest(http.MethodPost,
			tkv1.APIRoute, tkv1.RouteDetails, d)
		if err != nil {
			return nil, err
		}
		p.cache.Set(cacheKey, responseBody)
	}

	var dr tkv1.DetailsReply
	err = json.Unmarshal(responseBody, &dr)
	if err != nil {
		return nil, fmt.Errorf("Could not unmarshal DetailsReply: %v",
			err)
	}

	// Verify VoteDetails.
	err = client.VoteDetailsVerify(*dr.Vote, serverPubKey)
	if err != nil {
		return nil, err
	}

	return &dr, nil
}

func (p *piv) voteResults(token, serverPubKey string) (*tkv1.ResultsReply, error) {
	r := tkv1.Results{
		Token: token,
	}
	responseBody, err := p.makeRequest(http.MethodPost,
		tkv1.APIRoute, tkv1.RouteResults, r)
	if err != nil {
		return nil, err
	}

	var rr tkv1.ResultsReply
	err = json.Unmarshal(responseBody, &rr)
	if err != nil {
		return nil, fmt.Errorf("Could not unmarshal ResultsReply: %v", err)
	}

	// Verify CastVoteDetails.
	for _, cvd := range rr.Votes {
		err = client.CastVoteDetailsVerify(cvd, serverPubKey)
		if err != nil {
			return nil, err
		}
	}

	return &rr, nil
}

// records sends a records API Records request and returns the reply.
func (p *piv) records(tokens []string, serverPubKey string) (*rcv1.RecordsReply, error) {
	// Prepare request
	reqs := make([]rcv1.RecordRequest, 0, len(tokens))
	for _, t := range tokens {
		reqs = append(reqs, rcv1.RecordRequest{
			Token: t,
			Filenames: []string{
				piv1.FileNameProposalMetadata,
			},
		})
	}

	// Send request
	responseBody, err := p.makeRequest(http.MethodPost, rcv1.APIRoute,
		rcv1.RouteRecords, rcv1.Records{
			Requests: reqs,
		})
	if err != nil {
		return nil, err
	}

	var rsr rcv1.RecordsReply
	err = json.Unmarshal(responseBody, &rsr)
	if err != nil {
		return nil, fmt.Errorf("Could not unmarshal RecordsReply: %v",
			err)
	}

	return &rsr, nil
}

// votePolicy sends a ticketvote API Policy request and returns the reply.
func (p *piv) votePolicy() (*tkv1.PolicyReply, error) {
	// Send request
	responseBody, err := p.makeRequest(http.MethodPost, tkv1.APIRoute,
		tkv1.RoutePolicy, tkv1.Policy{})
	if err != nil {
		return nil, err
	}

	var pr tkv1.PolicyReply
	err = json.Unmarshal(responseBody, &pr)
	if err != nil {
		return nil, fmt.Errorf("Could not unmarshal RecordsReply: %v",
			err)
	}

	return &pr, nil
}

func (p *piv) names(tokens ...string) (names map[string]string, err error) {
	version, err := p.getVersion()
	if err != nil {
		return nil, err
	}
	serverPubKey := version.PubKey
	names = make(map[string]string)
	reply, err := p.records(tokens, serverPubKey)
	if err != nil {
		return nil, err
	}

	// Get proposal metadata and store proposal name in map.
	for token, record := range reply.Records {
		md, err := client.ProposalMetadataDecode(record.Files)
		if err != nil {
			return nil, err
		}
		names[token] = md.Name
	}
	return names, err
}

func (p *piv) inventory() error {
	// Get server public key to verify replies.
	version, err := p.getVersion()
	if err != nil {
		return err
	}
	serverPubKey := version.PubKey

	// Inventory route is paginated, therefore we keep fetching
	// until we receive a patch with number of records smaller than the
	// ticketvote's declared page size. The page size is retrieved from
	// the ticketvote API Policy route.
	vp, err := p.votePolicy()
	if err != nil {
		return err
	}
	pageSize := vp.InventoryPageSize
	page := uint32(1)
	var tokens []string
	for {
		ir, err := p._inventory(tkv1.Inventory{
			Page:   page,
			Status: tkv1.VoteStatusStarted,
		})
		if err != nil {
			return err
		}
		pageTokens := ir.Vetted[tkv1.VoteStatuses[tkv1.VoteStatusStarted]]
		tokens = append(tokens, pageTokens...)
		if uint32(len(pageTokens)) < pageSize {
			break
		}
		page++
	}

	// Print empty message in case no active votes found.
	if len(tokens) == 0 {
		fmt.Printf("No active votes found.\n")
		return nil
	}

	// Retrieve the proposals metadata and store proposal names in a
	// map[token] => name.
	names := make(map[string]string, len(tokens))
	remainingTokens := tokens
	// As the records API Records route is paged, we need to fetch the proposals
	// metadata page by page.
	for len(remainingTokens) != 0 {
		var page []string
		if len(remainingTokens) > rcv1.RecordsPageSize {
			// If the number of remaining tokens to fetch exceeds the page size, we
			// get the next page and keep the rest for the next iteration.
			page = remainingTokens[:rcv1.RecordsPageSize]
			remainingTokens = remainingTokens[rcv1.RecordsPageSize:]
		} else {
			// If the number of remaining tokens to fetch is equal or smaller than
			// the page size then that's the last page.
			page = remainingTokens
			remainingTokens = []string{}
		}

		// Fetch page of records
		reply, err := p.records(page, serverPubKey)
		if err != nil {
			return err
		}

		// Get proposal metadata and store proposal name in map.
		for token, record := range reply.Records {
			md, err := client.ProposalMetadataDecode(record.Files)
			if err != nil {
				return nil
			}
			names[token] = md.Name
		}
	}

	for _, t := range tokens {
		// Get vote details.
		dr, err := p.voteDetails(t, serverPubKey)
		if err != nil {
			return err
		}
		// Ensure eligibility
		tix, err := convertTicketHashes(dr.Vote.EligibleTickets)
		if err != nil {
			fmt.Printf("Ticket pool corrupt: %v %v\n",
				dr.Vote.Params.Token, err)
			continue
		}
		ctres, err := p.wallet.CommittedTickets(p.ctx,
			&pb.CommittedTicketsRequest{
				Tickets: tix,
			})
		if err != nil {
			fmt.Printf("Ticket pool verification: %v %v\n",
				dr.Vote.Params.Token, err)
			continue
		}

		// Bail if there are no eligible tickets
		if len(ctres.TicketAddresses) == 0 {
			fmt.Printf("No eligible tickets: %v\n", dr.Vote.Params.Token)
		}

		// voteResults provides a list of the votes that have already been cast.
		// Use these to filter out the tickets that have already voted.
		rr, err := p.voteResults(dr.Vote.Params.Token, serverPubKey)
		if err != nil {
			fmt.Printf("Failed to obtain vote results for %v: %v\n",
				dr.Vote.Params.Token, err)
			continue
		}

		// Filter out tickets that have already voted or are otherwise
		// ineligible for the wallet to sign.  Note that tickets that have
		// already voted, but have an invalid signature are included so they
		// may be resubmitted.
		myVote, _, err := p.statsVotes(rr, ctres)
		if err != nil {
			fmt.Printf("Eligible vote filtering error: %v %v\n",
				dr.Vote.Params, err)
			continue
		}

		// Display vote bits
		fmt.Printf("Vote: %v\n", dr.Vote.Params.Token)
		fmt.Printf("  Proposal        : %v\n", names[t])
		fmt.Printf("  Start block     : %v\n", dr.Vote.StartBlockHeight)
		fmt.Printf("  End block       : %v\n", dr.Vote.EndBlockHeight)
		fmt.Printf("  Mask            : %v\n", dr.Vote.Params.Mask)
		fmt.Printf("  Eligible tickets: %v\n", len(ctres.TicketAddresses))
		fmt.Printf("  Eligible votes  : %v\n", myVote.Yet)
		fmt.Printf("  Voted yes  : %v\n", myVote.Yes)
		fmt.Printf("  Voted no   : %v\n", myVote.No)
		fmt.Printf("  Vote Option:\n")
		fmt.Printf("    politeiavoter vote %v percent yes 0.67 no 0.34\n", dr.Vote.Params.Token)
		fmt.Printf("    politeiavoter vote %v number yes 50 no 69\n", dr.Vote.Params.Token)
		fmt.Printf("    politeiavoter --voteduration=1h vote %v mirror\n", dr.Vote.Params.Token)
	}

	return nil
}

func (p *piv) stats() error {
	votingProposals, err := p.fetchActiveProposals()
	if err != nil {
		return err
	}

	// Get latest block
	latestBlock, err := p.GetBestBlock()
	if err != nil {
		return err
	}

	for _, vp := range votingProposals {

		endHeight := int32(vp.Vote.EndBlockHeight)

		// Sanity, check if vote has expired
		if latestBlock > endHeight {
			fmt.Printf("Vote expired: current %v > end %v %v\n",
				endHeight, latestBlock, vp.Vote.Params.Token)
			continue
		}

		remainBlocks := endHeight - latestBlock
		estRemainBlockTime := time.Duration(remainBlocks) * activeNetParams.TargetTimePerBlock
		timeEnd := time.Now().Add(estRemainBlockTime)
		fmt.Printf("Token: %s \tRemaining blocks: %v\tEst end date/time: %v\n", vp.Vote.Params.Token, remainBlocks, viewTime(timeEnd))

		// gather totals for proposal
		sepTicket, err := p.sortTicketsForProposal(vp)
		if err != nil {
			return err
		}

		//checks to make sure we have everything set
		if (sepTicket.TotalYes + sepTicket.TotalNo) != (sepTicket.TotalVoted) {
			return fmt.Errorf("total yes+no %v+%v does not equal totalvoted %v", sepTicket.TotalYes, sepTicket.TotalNo, sepTicket.TotalVoted)
		}
		if (sepTicket.TotalVoted + sepTicket.TotalRemain) != len(vp.Vote.EligibleTickets) {
			return fmt.Errorf("total voted+remain %v+%v does not equal total eligible %v", sepTicket.TotalVoted, sepTicket.TotalRemain, len(vp.Vote.EligibleTickets))
		}
		if (sepTicket.TotalYes + sepTicket.TotalNo + sepTicket.TotalRemain) != len(vp.Vote.EligibleTickets) {
			return fmt.Errorf("total voted yes+no+remain %v+%v+%v does not equal total eligible %v", sepTicket.TotalYes, sepTicket.TotalNo, sepTicket.TotalRemain, len(vp.Vote.EligibleTickets))
		} //more checks to come I am sure

		//get percs for prints
		totalEligible := len(vp.Vote.EligibleTickets)
		totalYesPerc := (float64(sepTicket.TotalYes) / float64(totalEligible)) * 100
		totalVotedPerc := (float64(sepTicket.TotalVoted) / float64(totalEligible)) * 100
		totalRemainPerc := (float64(sepTicket.TotalRemain) / float64(totalEligible)) * 100
		notOurYesPerc := (float64(sepTicket.NotOurYes) / float64(totalEligible)) * 100
		notOurVotedPerc := (float64(sepTicket.NotOurTotalVoted) / float64(totalEligible)) * 100
		notOurRemainPerc := (float64(sepTicket.NotOurTotalRemain) / float64(totalEligible)) + 100
		ourYesPerc := (float64(sepTicket.OurYes) / float64(totalEligible)) * 100
		ourVotedPerc := (float64(sepTicket.OurTotalVoted) / float64(totalEligible)) * 100
		ourRemainPerc := (float64(sepTicket.OurAvailableToVote) / float64(totalEligible)) * 100
		//TODO: put in prints based on separateTickets values

		fmt.Printf("Total: Yes %v  No %v (%.0f%% approval)  Voted %v (%.1f%%)  Remain %v (%.1f%%)\n",
			sepTicket.TotalYes, sepTicket.TotalNo, totalYesPerc, sepTicket.TotalVoted, totalVotedPerc, sepTicket.TotalRemain, totalRemainPerc)
		fmt.Printf("Public: Yes %v  No %v (%.2f%% approval)  Voted %v (%.2f%%)  Remain %v (%.1f%%)\n",
			sepTicket.NotOurYes, sepTicket.NotOurNo, notOurYesPerc, sepTicket.NotOurTotalVoted, notOurVotedPerc, sepTicket.NotOurTotalRemain, notOurRemainPerc)
		fmt.Printf("Me: Yes %v  No %v (%.2f%% approval)  Voted %v (%.2f%%)  Remain %v (%.1f%%)\n",
			sepTicket.OurYes, sepTicket.OurNo, ourYesPerc, sepTicket.OurTotalVoted, ourVotedPerc, sepTicket.OurAvailableToVote, ourRemainPerc)
	}

	return nil
}

type ErrRetry struct {
	At   string      `json:"at"`   // where in the code
	Body []byte      `json:"body"` // http body if we have one
	Code int         `json:"code"` // http code
	Err  interface{} `json:"err"`  // underlying error
}

func (e ErrRetry) Error() string {
	return fmt.Sprintf("retry error: %v (%v) %v", e.Code, e.At, e.Err)
}

// sendVoteFail isa test function that will fail a Ballot call with a retryable
// error.
func (p *piv) sendVoteFail(ballot *tkv1.CastBallot) (*tkv1.CastVoteReply, error) {
	return nil, ErrRetry{
		At: "sendVoteFail",
	}
}

func (p *piv) sendVote(ballot *tkv1.CastBallot) (*tkv1.CastVoteReply, error) {
	if len(ballot.Votes) != 1 {
		return nil, fmt.Errorf("sendVote: only one vote allowed")
	}

	responseBody, err := p.makeRequest(http.MethodPost,
		tkv1.APIRoute, tkv1.RouteCastBallot, ballot)
	if err != nil {
		return nil, err
	}

	var vr tkv1.CastBallotReply
	err = json.Unmarshal(responseBody, &vr)
	if err != nil {
		return nil, fmt.Errorf("Could not unmarshal "+
			"CastVoteReply: %v", err)
	}
	if len(vr.Receipts) != 1 {
		// Should be impossible
		return nil, fmt.Errorf("sendVote: invalid receipt count %v",
			len(vr.Receipts))
	}

	return &vr.Receipts[0], nil
}

// dumpComplete dumps the completed votes in this run.
func (p *piv) dumpComplete() {
	p.RLock()
	defer p.RUnlock()

	fmt.Printf("Completed votes (%v):\n", len(p.ballotResults))
	for _, v := range p.ballotResults {
		fmt.Printf("  %v %v\n", v.Ticket, v.ErrorCode)
	}
}

func (p *piv) dumpQueue() {
	p.RLock()
	defer p.RUnlock()

	panic("dumpQueue")
}

// dumpTogo dumps the votes that have not been cast yet.
func (p *piv) dumpTogo() {
	p.RLock()
	defer p.RUnlock()

	panic("dumpTogo")
}

func (p *piv) buildVotesToCast(token string, ctres *pb.CommittedTicketsResponse, qtyY, qtyN int, voteBitY, voteBitN string) (yesVotes, noVotes, allVotes []*tkv1.CastVote, err error) {
	var voteY, voteN int
	//var votesToCast []tkv1.CastVote
	for _, v := range ctres.TicketAddresses {
		if voteY == qtyY && voteN == qtyN {
			break
		}
		h, err := chainhash.NewHash(v.Ticket)
		if err != nil {
			return nil, nil, nil, err
		}
		var voteBit string
		if voteY < qtyY && voteN < qtyN {
			choice, err := randomInt64(0, 2)
			if err != nil {
				return nil, nil, nil, err
			}
			if choice == 1 {
				voteY++
				voteBit = voteBitY
			} else {
				voteN++
				voteBit = voteBitN
			}
		} else {
			if voteY < qtyY {
				voteY++
				voteBit = voteBitY
			} else {
				voteN++
				voteBit = voteBitN
			}
		}
		vote := &tkv1.CastVote{
			Token:   token,
			Ticket:  h.String(),
			VoteBit: voteBit,
			// Signature set from reply below.
		}
		allVotes = append(allVotes, vote)
		if voteBit == voteBitY {
			yesVotes = append(yesVotes, vote)
		} else {
			noVotes = append(noVotes, vote)
		}
	}
	return yesVotes, noVotes, allVotes, nil
}

func (p *piv) _vote(args []string) error {
	token := args[0]
	vs, err := p._summary(token)
	if err != nil {
		return err
	}
	bestBlock, err := p.GetBestBlock()
	if err != nil {
		return err
	}
	remainingBlock := vs.EndBlockHeight - uint32(bestBlock)
	if remainingBlock <= 0 {
		return nil
	}
	qtyY, qtyN, voted, total, err := p.validateArguments(args)
	if err != nil {
		return err
	}
	if voted == total {
		return fmt.Errorf("you voted all your tickets")
	}
	if qtyY == 0 && qtyN == 0 && !p.cfg.isMirror {
		return fmt.Errorf("request vote yes and no = 0")
	}
	err = p._processVote(token, qtyY, qtyN)
	if p.cfg.isMirror {
		for {
			select {
			case <-time.After(p.cfg.voteDuration):
				return p._vote(args)
			case <-p.ctx.Done():
				return nil
			default:
			}
		}
	}
	return err
}

func (p *piv) _processVote(token string, qtyY, qtyN int) error {
	passphrase, err := p.walletPassphrase()
	if err != nil {
		return err
	}
	// This assumes the account is an HD account.
	_, err = p.wallet.GetAccountExtendedPrivKey(p.ctx,
		&pb.GetAccountExtendedPrivKeyRequest{
			AccountNumber: 0, // TODO: make a config flag
			Passphrase:    passphrase,
		})
	if err != nil {
		return err
	}

	seed, err := generateSeed()
	if err != nil {
		return err
	}

	// Verify vote is still active
	vs, err := p._summary(token)
	if err != nil {
		return err
	}

	if vs.Status != tkv1.VoteStatusStarted {
		return fmt.Errorf("proposal vote is not active: %v", vs.Status)
	}

	// Get server public key by calling version request.
	v, err := p.getVersion()
	if err != nil {
		return err
	}

	// Get vote details.
	dr, err := p.voteDetails(token, v.PubKey)
	if err != nil {
		return err
	}

	var (
		voteBitY, voteBitN string
	)
	for _, vv := range dr.Vote.Params.Options {
		if vv.ID == "yes" {
			voteBitY = strconv.FormatUint(vv.Bit, 16)
		}
		if vv.ID == "no" {
			voteBitN = strconv.FormatUint(vv.Bit, 16)
		}
	}

	// Find eligible tickets
	tix, err := convertTicketHashes(dr.Vote.EligibleTickets)
	if err != nil {
		return fmt.Errorf("ticket pool corrupt: %v %v",
			token, err)
	}
	ctres, err := p.wallet.CommittedTickets(p.ctx,
		&pb.CommittedTicketsRequest{
			Tickets: tix,
		})
	if err != nil {
		return fmt.Errorf("ticket pool verification: %v %v",
			token, err)
	}
	if len(ctres.TicketAddresses) == 0 && p.cfg.EmulateVote == 0 {
		return fmt.Errorf("no eligible tickets found")
	}

	// voteResults a list of the votes that have already been cast. We use these
	// to filter out the tickets that have already voted.
	rr, err := p.voteResults(token, v.PubKey)
	if err != nil {
		return err
	}

	// Filter out tickets that have already voted or are otherwise ineligible
	// for the wallet to sign.  Note that tickets that have already voted, but
	// have an invalid signature are included so they may be resubmitted.
	var eligible []*pb.CommittedTicketsResponse_TicketAddress
	if p.cfg.EmulateVote > 0 {
		for i := 0; i < p.cfg.EmulateVote; i++ {
			eligible = append(eligible, &pb.CommittedTicketsResponse_TicketAddress{
				Ticket:  tix[i],
				Address: dr.Vote.EligibleTickets[i],
			})
		}
	} else {
		_, _, eligible, err = p.eligibleVotes(rr, ctres)
		if err != nil {
			return err
		}
	}

	eligibleLen := len(eligible)
	if eligibleLen == 0 {
		return fmt.Errorf("no eligible tickets found")
	}
	r := rand.New(rand.NewSource(seed))
	// Fisher-Yates shuffle the ticket addresses.
	for i := 0; i < eligibleLen; i++ {
		// Pick a number between current index and the end.
		j := r.Intn(eligibleLen-i) + i
		eligible[i], eligible[j] = eligible[j], eligible[i]
	}
	ctres.TicketAddresses = eligible

	// Create unsigned votes to cast.
	yesVotes, noVotes, allVotes, err := p.buildVotesToCast(token, ctres, qtyY, qtyN, voteBitY, voteBitN)
	if err != nil {
		return err
	}
	if p.cfg.EmulateVote <= 0 {
		// Sign all messages that comprise the votes.
		sm := &pb.SignMessagesRequest{
			Passphrase: passphrase,
			Messages:   make([]*pb.SignMessagesRequest_Message, 0, len(allVotes)),
		}
		for k, v := range allVotes {
			//cv := &v
			msg := v.Token + v.Ticket + v.VoteBit
			sm.Messages = append(sm.Messages, &pb.SignMessagesRequest_Message{
				Address: ctres.TicketAddresses[k].Address,
				Message: msg,
			})
		}
		smr, err := p.wallet.SignMessages(p.ctx, sm)
		if err != nil {
			return err
		}
		// Assert arrays are same length.
		if len(allVotes) != len(smr.Replies) {
			return fmt.Errorf("assert len(votesToCast)) != len(Replies) -- %v "+
				"!= %v", len(allVotes), len(smr.Replies))
		}

		// Ensure all the signatures worked while simultaneously setting the
		// signature in the vote.
		for k, v := range smr.Replies {
			if v.Error != "" {
				return fmt.Errorf("signature failed index %v: %v", k, v.Error)
			}

			allVotes[k].Signature = hex.EncodeToString(v.Signature)
		}
	}

	// Trickle in the votes if specified
	err = p.setupVoteDuration(*vs)
	if err != nil {
		return err
	}

	// Trickle votes
	return p.alarmTrickler(token, allVotes, yesVotes, noVotes, voteBitY, voteBitN)
}

// setupVoteDuration sets up the duration that will be used for trickling
// votes. The user can either set a duration manually using the --voteduration
// setting or this function will calculate a duration. The calculated duration
// is the remaining time left in the vote minus the --hoursprior setting.
func (p *piv) setupVoteDuration(vs tkv1.Summary) error {
	var (
		blocksLeft     = int64(vs.EndBlockHeight) - int64(vs.BestBlock)
		blockTime      = activeNetParams.TargetTimePerBlock
		timeLeftInVote = time.Duration(blocksLeft) * blockTime
		timePassInVote = time.Duration(int64(vs.BestBlock)-int64(vs.StartBlockHeight)) * blockTime
	)
	p.cfg.startTime = time.Now()
	if p.cfg.Resume {
		p.cfg.startTime = time.Now().Add(-timePassInVote)
	}
	switch {
	case p.cfg.voteDuration.Seconds() > 0:
		// A vote duration was provided
		if p.cfg.voteDuration > timeLeftInVote {
			return fmt.Errorf("the provided --voteduration of %v is "+
				"greater than the remaining time in the vote of %v",
				p.cfg.voteDuration, timeLeftInVote)
		}

	case p.cfg.voteDuration.Seconds() == 0:
		// A vote duration was not provided. The vote duration is set to
		// the remaining time in the vote minus the hours prior setting.
		p.cfg.voteDuration = timeLeftInVote - p.cfg.hoursPrior
		if p.cfg.Resume {
			p.cfg.voteDuration = timeLeftInVote + timePassInVote - p.cfg.hoursPrior
		}

		// Force the user to manually set the vote duration when the
		// calculated duration is under 24h.
		if p.cfg.voteDuration < (24 * time.Hour) {
			return fmt.Errorf("there is only %v left in the vote; when "+
				"the remaining time is this low you must use --voteduration "+
				"to manually set the duration that will be used to trickle "+
				"in your votes, example --voteduration=6h", timeLeftInVote)
		}

	default:
		// Should not be possible
		return fmt.Errorf("invalid vote duration %v", p.cfg.voteDuration)
	}

	return nil
}

// validateArguments ensure the input parameter is correct and return
// quantity vote(yes/no) will be voted, number voted and total eligible votes
func (p *piv) validateArguments(args []string) (qtyYes, qtyNo, voted, total int, err error) {
	var rateYes float64
	var rateNo float64
	// we have at least 2 arguments: token id and mode vote
	var token = args[0]
	var mode = args[1]
	me, them, err := p.getTotalVotes(token)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	var voteYes, voteNo int
	switch mode {
	case voteModeMirror:
		if len(args) != 2 {
			return 0, 0, 0, 0, fmt.Errorf("invalid arguments")
		}
		p.cfg.isMirror = true
		if p.cfg.voteDuration == 0 {
			return 0, 0, 0, 0, fmt.Errorf("mirror mode require voteduration is set")
		}
		if p.cfg.EmulateVote > 0 {
			return 0, 0, 0, 0, fmt.Errorf("mirror mode is not worked with emulatevote")
		}
		rateYes = float64(them.Yes) / float64(them.Total())
		rateNo = float64(them.No) / float64(them.Total())
	case voteModeNumber:
		if len(args) != 5 {
			return 0, 0, 0, 0, fmt.Errorf("vote: not enough arguments %v", args)
		}
		if args[1] != "yes" {
			return 0, 0, 0, 0, fmt.Errorf("invalid argument, see the example to correct it")
		}
		if args[3] != "no" {
			return 0, 0, 0, 0, fmt.Errorf("invalid argument, see the example to correct it")
		}
		voteYes, _ = strconv.Atoi(args[2])
		voteNo, _ = strconv.Atoi(args[4])
		if voteYes+voteNo > me.Total() {
			if len(args) != 5 {
				return 0, 0, 0, 0,
					fmt.Errorf("entered amount is greater than your total own votes: %d", me.Total())
			}
		}
	case voteModePercent:
		if len(args) != 5 {
			return 0, 0, 0, 0, fmt.Errorf("vote: not enough arguments %v", args)
		}
		if args[1] != "yes" {
			return 0, 0, 0, 0, fmt.Errorf("invalid argument, see the example to correct it")
		}
		if args[3] != "no" {
			return 0, 0, 0, 0, fmt.Errorf("invalid argument, see the example to correct it")
		}
		rateYes, _ = strconv.ParseFloat(args[2], 64)
		rateNo, _ = strconv.ParseFloat(args[4], 64)
		if rateYes < 0 || rateNo < 0 {
			return 0, 0, 0, 0, fmt.Errorf("rate must be > 0 and < 1")
		}
		if rateYes+rateNo > 1 {
			return 0, 0, 0, 0, fmt.Errorf("total rate yes and rate no is greater than 1")
		}
	default:
		return 0, 0, 0, 0, fmt.Errorf("mode [%s] is not supported", mode)
	}

	if mode != voteModeNumber {
		// calculate number vote from the rate
		roughYes := float64(total) * rateYes
		roughNo := float64(total) * rateNo
		voteYes = int(math.Round(roughYes))
		voteNo = int(math.Round(roughNo))
	}

	var votedY, votedN int
	if p.cfg.EmulateVote > 0 {
		total = p.cfg.EmulateVote
	} else {
		votedY, votedN, total = me.Yes, me.No, me.Total()
	}

	if !p.cfg.isMirror {
		if voteYes < votedY {
			return 0, 0, 0, 0, fmt.Errorf("resume: require vote %d yes but voted %d from previous session", voteYes, votedY)
		}
		if voteNo < votedN {
			return 0, 0, 0, 0, fmt.Errorf("resume: require vote %d no but voted %d from previous session", voteNo, votedN)
		}
	}
	qtyYes = voteYes - votedY
	if qtyYes < 0 {
		qtyYes = 0
	}
	qtyNo = voteNo - votedN
	if qtyNo < 0 {
		qtyNo = 0
	}
	return qtyYes, qtyNo, votedY + votedN, total, nil
}

func (p *piv) getTotalVotes(token string) (me, them *VoteStats, err error) {
	passphrase, err := p.walletPassphrase()
	if err != nil {
		return nil, nil, err
	}
	// This assumes the account is an HD account.
	_, err = p.wallet.GetAccountExtendedPrivKey(p.ctx,
		&pb.GetAccountExtendedPrivKeyRequest{
			AccountNumber: 0, // TODO: make a config flag
			Passphrase:    passphrase,
		})
	if err != nil {
		return nil, nil, err
	}

	// Get server public key by calling version request.
	v, err := p.getVersion()
	if err != nil {
		return nil, nil, err
	}

	// Get vote details.
	dr, err := p.voteDetails(token, v.PubKey)
	if err != nil {
		return nil, nil, err
	}
	// Find eligble tickets
	tix, err := convertTicketHashes(dr.Vote.EligibleTickets)
	if err != nil {
		return nil, nil, fmt.Errorf("ticket pool corrupt: %v %v",
			token, err)
	}
	ctres, err := p.wallet.CommittedTickets(p.ctx,
		&pb.CommittedTicketsRequest{
			Tickets: tix,
		})
	if err != nil {
		return nil, nil, fmt.Errorf("ticket pool verification: %v %v",
			token, err)
	}
	if len(ctres.TicketAddresses) == 0 {
		return nil, nil, fmt.Errorf("no eligible tickets found")
	}

	// voteResults a list of the votes that have already been cast. We use these
	// to filter out the tickets that have already voted.
	rr, err := p.voteResults(token, v.PubKey)
	if err != nil {
		return nil, nil, err
	}
	return p.statsVotes(rr, ctres)
}

func (p *piv) vote(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("not enough arguments")
	}
	var token = args[0]
	names, err := p.names(token)
	if err != nil {
		return err
	} else {
		fmt.Printf("Voting on      : %s\n", names[token])
	}

	err = p._vote(args) //token, qtyYes, qtyNo)
	// we return err after printing details

	// Verify vote replies. Already voted errors are not
	// considered to be failures because they occur when
	// a network error or dropped client connection causes
	// politeiavoter to incorrectly think that the first
	// attempt to cast the vote failed. politeiavoter will
	// attempt to retry the vote that it has already
	// successfully cast, resulting in the already voted
	// error.
	var alreadyVoted int
	failedReceipts := make([]tkv1.CastVoteReply, 0,
		len(p.ballotResults))
	for _, v := range p.ballotResults {
		if v.ErrorCode == nil {
			continue
		}
		if *v.ErrorCode == tkv1.VoteErrorTicketAlreadyVoted {
			alreadyVoted++
			continue
		}
		failedReceipts = append(failedReceipts, v)
	}

	log.Debugf("%v already voted errors found; these are "+
		"counted as being successful", alreadyVoted)

	fmt.Printf("Votes succeeded: %v(yes-%d/no-%d)\n", len(p.ballotResults)-
		len(failedReceipts), p.votedYes, p.votedNo)
	fmt.Printf("Votes failed   : %v\n", len(failedReceipts))
	notCast := cap(p.ballotResults) - len(p.ballotResults)
	if notCast > 0 {
		fmt.Printf("Votes not cast : %v\n", notCast)
	}
	for _, v := range failedReceipts {
		fmt.Printf("Failed vote    : %v %v\n",
			v.Ticket, v.ErrorContext)
	}

	return err
}

func (p *piv) _summary(token string) (*tkv1.Summary, error) {
	p.mux.Lock()
	defer p.mux.Unlock()
	if summary, ok := p.summaries[token]; ok {
		return &summary, nil
	}
	responseBody, err := p.makeRequest(http.MethodPost,
		tkv1.APIRoute, tkv1.RouteSummaries,
		tkv1.Summaries{Tokens: []string{token}})
	if err != nil {
		return nil, err
	}

	var sr tkv1.SummariesReply
	err = json.Unmarshal(responseBody, &sr)
	if err != nil {
		return nil, fmt.Errorf("Could not unmarshal SummariesReply: %v", err)
	}
	if summary, ok := sr.Summaries[token]; ok {
		p.summaries[token] = summary
		return &summary, nil
	}
	return nil, fmt.Errorf("proposal does not exist: %v", token)
}

func (p *piv) tally(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("tally: not enough arguments %v", args)
	}

	// Get server public key by calling version.
	v, err := p.getVersion()
	if err != nil {
		return err
	}

	token := args[0]
	t, err := p.voteResults(token, v.PubKey)
	if err != nil {
		return err
	}

	// tally votes
	count := make(map[uint64]uint)
	var total uint
	for _, v := range t.Votes {
		bits, err := strconv.ParseUint(v.VoteBit, 10, 64)
		if err != nil {
			return err
		}
		count[bits]++
		total++
	}

	if total == 0 {
		return fmt.Errorf("no votes recorded")
	}

	// Get vote details to dump vote options.
	dr, err := p.voteDetails(token, v.PubKey)
	if err != nil {
		return err
	}

	// Dump
	for _, vo := range dr.Vote.Params.Options {
		fmt.Printf("Vote Option:\n")
		fmt.Printf("  Id                   : %v\n", vo.ID)
		fmt.Printf("  Description          : %v\n",
			vo.Description)
		fmt.Printf("  Bit                  : %v\n", vo.Bit)
		vr := count[vo.Bit]
		fmt.Printf("  Votes received       : %v\n", vr)
		if total == 0 {
			continue
		}
		fmt.Printf("  Percentage           : %v%%\n",
			(float64(vr))/float64(total)*100)
	}

	return nil
}

type failedTuple struct {
	Time  JSONTime
	Votes tkv1.CastBallot `json:"votes"`
	Error ErrRetry
}

func decodeFailed(filename string, failed map[string][]failedTuple) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(f)

	var (
		ft     *failedTuple
		ticket string
	)
	state := 0
	for {
		switch state {
		case 0:
			ft = &failedTuple{}
			err = d.Decode(&ft.Time)
			if err != nil {
				// Only expect EOF in state 0
				if err == io.EOF {
					goto exit
				}
				return fmt.Errorf("decode time (%v): %v",
					d.InputOffset(), err)
			}
			state = 1

		case 1:
			err = d.Decode(&ft.Votes)
			if err != nil {
				return fmt.Errorf("decode cast votes (%v): %v",
					d.InputOffset(), err)
			}

			// Save ticket
			if len(ft.Votes.Votes) != 1 {
				// Should not happen
				return fmt.Errorf("decode invalid length %v",
					len(ft.Votes.Votes))
			}
			ticket = ft.Votes.Votes[0].Ticket

			state = 2

		case 2:
			err = d.Decode(&ft.Error)
			if err != nil {
				return fmt.Errorf("decode error retry (%v): %v",
					d.InputOffset(), err)
			}

			// Add to map
			if ticket == "" {
				return fmt.Errorf("decode no ticket found")
			}
			//fmt.Printf("failed ticket %v\n", ticket)
			failed[ticket] = append(failed[ticket], *ft)

			// Reset statemachine
			ft = &failedTuple{}
			ticket = ""
			state = 0
		}
	}

exit:
	return nil
}

type successTuple struct {
	Time   JSONTime
	Result tkv1.CastVoteReply
}

func decodeSuccess(filename string, success map[string][]successTuple) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(f)

	var st *successTuple
	state := 0
	for {
		switch state {
		case 0:
			st = &successTuple{}
			err = d.Decode(&st.Time)
			if err != nil {
				// Only expect EOF in state 0
				if err == io.EOF {
					goto exit
				}
				return fmt.Errorf("decode time (%v): %v",
					d.InputOffset(), err)
			}
			state = 1

		case 1:
			err = d.Decode(&st.Result)
			if err != nil {
				return fmt.Errorf("decode cast votes (%v): %v",
					d.InputOffset(), err)
			}

			// Add to map
			ticket := st.Result.Ticket
			if ticket == "" {
				return fmt.Errorf("decode no ticket found")
			}

			//fmt.Printf("success ticket %v\n", ticket)
			success[ticket] = append(success[ticket], *st)

			// Reset statemachine
			st = &successTuple{}
			state = 0
		}
	}

exit:
	return nil
}

type workTuple struct {
	Time  JSONTime
	Votes []voteAlarm
}

func decodeWork(filename string, work map[string][]workTuple) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(f)

	var (
		wt *workTuple
		t  string
	)
	state := 0
	for {
		switch state {
		case 0:
			wt = &workTuple{}
			err = d.Decode(&wt.Time)
			if err != nil {
				// Only expect EOF in state 0
				if err == io.EOF {
					goto exit
				}
				return fmt.Errorf("decode time (%v): %v",
					d.InputOffset(), err)
			}
			t = wt.Time.Time
			state = 1

		case 1:
			err = d.Decode(&wt.Votes)
			if err != nil {
				return fmt.Errorf("decode votes (%v): %v",
					d.InputOffset(), err)
			}

			// Add to map
			if t == "" {
				return fmt.Errorf("decode no time found")
			}

			work[t] = append(work[t], *wt)

			// Reset statemachine
			wt = &workTuple{}
			t = ""
			state = 0
		}
	}

exit:
	return nil
}

func (p *piv) verifyVote(vote string) error {
	// Vote directory
	dir := filepath.Join(p.cfg.voteDir, vote)

	// See if vote is ongoing
	vs, err := p._summary(vote)
	if err != nil {
		return fmt.Errorf("could not obtain proposal status: %v",
			err)
	}
	if vs.Status != tkv1.VoteStatusFinished &&
		vs.Status != tkv1.VoteStatusRejected &&
		vs.Status != tkv1.VoteStatusApproved {
		return fmt.Errorf("proposal vote not finished: %v",
			tkv1.VoteStatuses[vs.Status])
	}

	// Get server public key.
	v, err := p.getVersion()
	if err != nil {
		return err
	}

	// Get and cache vote results.
	voteResultsFilename := filepath.Join(dir, ".voteresults")
	if !util.FileExists(voteResultsFilename) {
		rr, err := p.voteResults(vote, v.PubKey)
		if err != nil {
			return fmt.Errorf("failed to obtain vote results "+
				"for %v: %v\n", vote, err)
		}
		f, err := os.Create(voteResultsFilename)
		if err != nil {
			return fmt.Errorf("create cache: %v", err)
		}
		e := json.NewEncoder(f)
		err = e.Encode(rr)
		if err != nil {
			f.Close()
			_ = os.Remove(voteResultsFilename)
			return fmt.Errorf("encode cache: %v", err)
		}
		f.Close()
	}

	// Open cached vote results.
	f, err := os.Open(voteResultsFilename)
	if err != nil {
		return fmt.Errorf("open cache: %v", err)
	}
	d := json.NewDecoder(f)
	var rr tkv1.ResultsReply
	err = d.Decode(&rr)
	if err != nil {
		f.Close()
		return fmt.Errorf("decode cache: %v", err)
	}
	f.Close()

	// Get vote details.
	dr, err := p.voteDetails(vote, v.PubKey)
	if err != nil {
		return fmt.Errorf("failed to obtain vote details "+
			"for %v: %v\n", vote, err)
	}

	// Index vote results for more vroom vroom
	eligible := make(map[string]string,
		len(dr.Vote.EligibleTickets))
	for _, v := range dr.Vote.EligibleTickets {
		eligible[v] = "" // XXX
	}
	cast := make(map[string]string, len(rr.Votes))
	for _, v := range rr.Votes {
		cast[v.Ticket] = "" // XXX
	}

	// Create local work caches
	fa, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	failed := make(map[string][]failedTuple, 128)   // [ticket]result
	success := make(map[string][]successTuple, 128) // [ticket]result
	work := make(map[string][]workTuple, 128)       // [time]work

	fmt.Printf("== Checking vote %v\n", vote)
	for k := range fa {
		name := fa[k].Name()

		filename := filepath.Join(dir, name)
		switch {
		case strings.HasPrefix(name, failedJournal):
			err = decodeFailed(filename, failed)
			if err != nil {
				fmt.Printf("decodeFailed %v: %v\n", filename,
					err)
			}

		case strings.HasPrefix(name, successJournal):
			err = decodeSuccess(filename, success)
			if err != nil {
				fmt.Printf("decodeSuccess %v: %v\n", filename,
					err)
			}

		case strings.HasPrefix(name, workJournal):
			err = decodeWork(filename, work)
			if err != nil {
				fmt.Printf("decodeWork %v: %v\n", filename,
					err)
			}

		case name == ".voteresults":
			// Cache file, skip

		default:
			fmt.Printf("unknown journal: %v\n", name)
		}
	}

	// Count vote statistics
	type voteStat struct {
		ticket  string
		retries int
		failed  int
		success int
	}

	verbose := false
	failedVotes := make(map[string]voteStat)
	tickets := make(map[string]string, 128) // [time]
	for k := range work {
		wts := work[k]

		for kk := range wts {
			wt := wts[kk]

			for kkk := range wt.Votes {
				vi := wt.Votes[kkk]

				if kkk == 0 && verbose {
					fmt.Printf("Vote %v started: %v\n",
						vi.Vote.Token, wt.Time.Time)
				}

				ticket := vi.Vote.Ticket
				tickets[ticket] = "" // XXX
				vs := voteStat{
					ticket: ticket,
				}
				if f, ok := failed[ticket]; ok {
					vs.retries = len(f)
				}
				if s, ok := success[ticket]; ok {
					vs.success = len(s)
					if len(s) != 1 {
						fmt.Printf("multiple success:"+
							" %v %v\n", len(s),
							ticket)
					}
				} else {
					vs.failed = 1
					failedVotes[ticket] = vs
				}

				if verbose {
					fmt.Printf("  ticket: %v retries %v "+
						"success %v failed %v\n",
						vs.ticket, vs.retries,
						vs.success, vs.failed)
				}
			}
		}
	}

	noVote := 0
	failedVote := 0
	completedNotRecorded := 0
	for _, v := range failedVotes {
		reason := "Error"
		if v.retries == 0 {
			if _, ok := cast[v.ticket]; ok {
				completedNotRecorded++
				continue
			}
			reason = "Not attempted"
			noVote++
		}
		if v.failed != 0 {
			fmt.Printf("  FAILED: %v - %v\n", v.ticket, reason)
			failedVote++
			continue
		}
	}
	if noVote != 0 {
		fmt.Printf("  votes that were not attempted: %v\n", noVote)
	}
	if failedVote != 0 {
		fmt.Printf("  votes that failed: %v\n", failedVote)
	}
	if completedNotRecorded != 0 {
		fmt.Printf("  votes that completed but were not recorded: %v\n",
			completedNotRecorded)
	}

	// Cross check results
	eligibleNotFound := 0
	for ticket := range tickets {
		// Did politea see ticket
		if _, ok := eligible[ticket]; !ok {
			fmt.Printf("work ticket not eligble: %v\n", ticket)
			eligibleNotFound++
		}

		// Did politea complete vote
		_, successFound := success[ticket]
		_, failedFound := failedVotes[ticket]
		switch {
		case successFound && failedFound:
			fmt.Printf("  pi vote succeeded and failed, " +
				"impossible condition\n")
		case !successFound && failedFound:
			if _, ok := cast[ticket]; !ok {
				fmt.Printf("  pi vote failed: %v\n", ticket)
			}
		case successFound && !failedFound:
			// Vote succeeded on the first try
		case !successFound && !failedFound:
			fmt.Printf("  pi vote not seen: %v\n", ticket)
		}
	}

	if eligibleNotFound != 0 {
		fmt.Printf("  ineligible tickets: %v\n", eligibleNotFound)
	}

	// Print overall status
	fmt.Printf("  Total votes       : %v\n", len(tickets))
	fmt.Printf("  Successful votes  : %v\n", len(success)+
		completedNotRecorded)
	fmt.Printf("  Unsuccessful votes: %v\n", failedVote)
	if failedVote != 0 {
		fmt.Printf("== Failed votes on proposal %v\n", vote)
	} else {
		fmt.Printf("== NO failed votes on proposal %v\n", vote)
	}

	return nil
}

func (p *piv) verify(args []string) error {
	// Override 0 to list all possible votes.
	if len(args) == 0 {
		fa, err := os.ReadDir(p.cfg.voteDir)
		if err != nil {
			return err
		}
		fmt.Printf("Votes:\n")
		for k := range fa {
			_, err := hex.DecodeString(fa[k].Name())
			if err != nil {
				continue
			}
			fmt.Printf("  %v\n", fa[k].Name())
		}
	}

	if len(args) == 1 && args[0] == "ALL" {
		fa, err := os.ReadDir(p.cfg.voteDir)
		if err != nil {
			return err
		}
		for k := range fa {
			_, err := hex.DecodeString(fa[k].Name())
			if err != nil {
				continue
			}

			err = p.verifyVote(fa[k].Name())
			if err != nil {
				fmt.Printf("verifyVote: %v\n", err)
			}
		}

		return nil
	}

	for k := range args {
		_, err := hex.DecodeString(args[k])
		if err != nil {
			fmt.Printf("invalid vote: %v\n", args[k])
			continue
		}

		err = p.verifyVote(args[k])
		if err != nil {
			fmt.Printf("verifyVote: %v\n", err)
		}
	}

	return nil
}

func (p *piv) help(command string) {
	switch command {
	case cmdInventory:
		fmt.Fprintf(os.Stdout, "%s\n", inventoryHelpMsg)
	case cmdVote:
		fmt.Fprintf(os.Stdout, "%s\n", voteHelpMsg)
	case cmdTally:
		fmt.Fprintf(os.Stdout, "%s\n", tallyHelpMsg)
	case cmdTallyTable:
		fmt.Fprintf(os.Stdout, "%s\n", tallyTableHelpMsg)
	case cmdVerify:
		fmt.Fprintf(os.Stdout, "%s\n", verifyHelpMsg)
	}
}

func _main() error {
	appName := filepath.Base(os.Args[0])
	appName = strings.TrimSuffix(appName, filepath.Ext(appName))
	cfg, args, err := loadConfig(appName)
	if err != nil {
		usageMessage := fmt.Sprintf("Use %s -h to show usage", appName)
		fmt.Fprintln(os.Stderr, err)
		var e errSuppressUsage
		if !errors.As(err, &e) {
			fmt.Fprintln(os.Stderr, usageMessage)
		}
		return err
	}
	defer func() {
		if logRotator != nil {
			logRotator.Close()
		}
	}()
	if len(args) == 0 {
		err := fmt.Errorf("No command specified\n%s", listCmdMessage)
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	action := args[0]

	// Get a context that will be canceled when a shutdown signal has been
	// triggered either from an OS signal such as SIGINT (Ctrl+C) or from
	// another subsystem such as the RPC server.
	shutdownCtx := shutdownListener()

	// Contact WWW
	c, err := firstContact(shutdownCtx, cfg)
	if err != nil {
		err := fmt.Errorf("Network error: %v\n", err)
		fmt.Fprintln(os.Stderr, err)
		return err
	}
	// Close GRPC
	defer c.conn.Close()

	// Validate command
	switch action {
	case cmdInventory, cmdTally, cmdTallyTable, cmdVote, cmdStats:
		// These commands require a connection to a dcrwallet instance. Get
		// block height to validate GPRC creds.
		ar, err := c.wallet.Accounts(c.ctx, &pb.AccountsRequest{})
		if err != nil {
			err := fmt.Errorf("Error pulling wallet accounts: %v\n", err)
			fmt.Fprintln(os.Stderr, err)
			return err
		}
		log.Debugf("Current wallet height: %v", ar.CurrentBlockHeight)

	case cmdVerify, cmdHelp:
		// valid command, continue

	default:
		err := fmt.Errorf("Unrecognized command %q\n%s", action, listCmdMessage)
		fmt.Fprintln(os.Stderr, err)
		return err
	}

	// Run command
	switch action {
	case cmdInventory:
		err = c.inventory()
	case cmdStats:
		err = c.stats()
	case cmdVote:
		err = c.vote(args[1:])
	case cmdTally:
		err = c.tally(args[1:])
	case cmdTallyTable:
		err = c.tallyTable(args[1:])
	case cmdVerify:
		err = c.verify(args[1:])
	case cmdHelp:
		if len(args) < 2 {
			err := fmt.Errorf("No help command specified\n%s", listCmdMessage)
			fmt.Fprintln(os.Stderr, err)
			return err
		}
		c.help(args[1])
	}

	if err != nil {
		log.Error(err)
	}
	return err
}

func main() {
	if err := _main(); err != nil {
		os.Exit(1)
	}
}
