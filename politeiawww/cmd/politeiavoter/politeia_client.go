package main

import (
	pb "decred.org/dcrwallet/rpc/walletrpc"
	"encoding/json"
	"fmt"
	"github.com/decred/dcrd/chaincfg/chainhash"
	tkv1 "github.com/decred/politeia/politeiawww/api/ticketvote/v1"
	"github.com/decred/politeia/politeiawww/client"
	"math/rand"
	"net/http"
	"time"
)

const (
	ansicDateFormat = "2006-01-02 15:04:05"

	VoteBitYes = "2"
	VoteBitNo  = "1"
)

type ProposalTicketsSummary struct {
	OurAvailableToVoteTickets []string
	OurVotedTickets           []string
	OurYesTickets             []string
	OurNoTickets              []string
	NotOurVotedOnTickets      []string //does not include ours
	NotOurVotedOnYesTickets   []string //does not include ours
	NotOurVotedOnNoTickets    []string //does not include ours
	TotalYesTickets           []string
	TotalNoTickets            []string
	TotalRemainTickets        []string

	//counts
	TotalYes           int
	TotalNo            int
	TotalRemain        int
	TotalVoted         int
	NotOurYes          int
	NotOurNo           int
	NotOurTotalVoted   int
	NotOurTotalRemain  int
	OurYes             int
	OurNo              int
	OurTotalVoted      int
	OurAvailableToVote int
	OurTotal           int
}

func (p *piv) sortTicketsForProposal(voteDetails *tkv1.DetailsReply) (ticketCont ProposalTicketsSummary, err error) {

	token := voteDetails.Vote.Params.Token
	votesResults, err := p.fetchVoteResults(token)
	if err != nil {
		return ticketCont, err
	}

	// Find eligble tickets
	tix, err := convertTicketHashes(voteDetails.Vote.EligibleTickets)
	if err != nil {
		return ticketCont, fmt.Errorf("ticket pool corrupt: %v %v",
			token, err)
	}
	ctres, err := p.wallet.CommittedTickets(p.ctx,
		&pb.CommittedTicketsRequest{
			Tickets: tix,
		})
	if err != nil {
		return ticketCont, fmt.Errorf("ticket pool verification: %v %v",
			token, err)
	}
	if len(ctres.TicketAddresses) == 0 {
		return ticketCont, fmt.Errorf("no eligible tickets found")
	}

	// Put cast votes into a map so that we can
	// filter in linear time
	castVotes := make(map[string]tkv1.CastVoteDetails)
	for _, v := range votesResults.Votes {
		castVotes[v.Ticket] = v
	}

	// Filter out tickets that have already voted. If a ticket has
	// voted but the signature is invalid, resubmit the vote. This
	// could be caused by bad data on the server or if the server is
	// lying to the client.
	filtered := make([]*pb.CommittedTicketsResponse_TicketAddress, 0,
		len(ctres.TicketAddresses))
	alreadyVoted := make([]*pb.CommittedTicketsResponse_TicketAddress, 0,
		len(ctres.TicketAddresses))
	for _, t := range ctres.TicketAddresses {
		h, err := chainhash.NewHash(t.Ticket)
		if err != nil {
			return ticketCont, err
		}
		_, ok := castVotes[h.String()]
		if !ok {
			filtered = append(filtered, t)
		} else {
			alreadyVoted = append(alreadyVoted, t)
		}
	}
	filteredLen := len(filtered)
	if filteredLen == 0 {
		return ticketCont, fmt.Errorf("no eligible tickets found")
	}
	var seed int64
	seed, err = generateSeed()
	if err != nil {
		return ticketCont, err
	}

	r := rand.New(rand.NewSource(seed))
	// Fisher-Yates shuffle the ticket addresses.
	for i := 0; i < filteredLen; i++ {
		// Pick a number between current index and the end.
		j := r.Intn(filteredLen-i) + i
		filtered[i], filtered[j] = filtered[j], filtered[i]
	}
	ctres.TicketAddresses = filtered

	//available to vote tickets
	for _, v := range ctres.TicketAddresses {
		h, err := chainhash.NewHash(v.Ticket)
		if err != nil {
			return ticketCont, err
		}
		ticketCont.OurAvailableToVoteTickets = append(ticketCont.OurAvailableToVoteTickets, h.String())
	}

	//our already voted on tickets
	for _, v := range alreadyVoted {
		h, err := chainhash.NewHash(v.Ticket)
		if err != nil {
			return ticketCont, err
		}
		ticketCont.OurVotedTickets = append(ticketCont.OurVotedTickets, h.String())

		var foundOurVote bool
		for _, w := range votesResults.Votes {
			if w.Ticket == h.String() {
				foundOurVote = true
				if w.VoteBit == VoteBitNo { //NO
					ticketCont.OurNoTickets = append(ticketCont.OurNoTickets, h.String())
				} else if w.VoteBit == VoteBitYes { //YES
					ticketCont.OurYesTickets = append(ticketCont.OurYesTickets, h.String())
				} else { //UNKNOWN
					fmt.Printf("ERROR: votebit %s does not match 1 (no) or 2 (yes), ticket %s", w.VoteBit, h.String())
				}
			}
		}
		//throw error because we did not find this ticket in the already voted array and that should not happen
		if !foundOurVote {
			return ticketCont, fmt.Errorf("ERROR: we did not find ticket %s in already voted array, it should be though... ", h.String())
		}
	}

	//loop through all voted on tickets and filter out ours from the list
	var allCastVotes []string
	for _, v := range votesResults.Votes {

		allCastVotes = append(allCastVotes, v.Ticket)

		//All Tickets
		if v.VoteBit == "1" { //NO
			ticketCont.TotalNoTickets = append(ticketCont.TotalNoTickets, v.Ticket)
		} else if v.VoteBit == "2" { //YES
			ticketCont.TotalYesTickets = append(ticketCont.TotalYesTickets, v.Ticket)
		} else { //UNKNOWN
			fmt.Printf("ERROR: votebit %s does not match 1 (no) or 2 (yes) (alltickets), ticket %s", v.VoteBit, v.Ticket)
		}

		if !stringInSlice(v.Ticket, ticketCont.OurVotedTickets) {
			//not one of our tickets so all to the list of allvotedontickets
			ticketCont.NotOurVotedOnTickets = append(ticketCont.NotOurVotedOnTickets, v.Ticket)

			if v.VoteBit == "1" { //NO
				ticketCont.NotOurVotedOnNoTickets = append(ticketCont.NotOurVotedOnNoTickets, v.Ticket)
			} else if v.VoteBit == "2" { //YES
				ticketCont.NotOurVotedOnYesTickets = append(ticketCont.NotOurVotedOnYesTickets, v.Ticket)
			} else { //UNKNOWN
				fmt.Printf("ERROR: votebit %s does not match 1 (no) or 2 (yes) (notourticket), ticket %s", v.VoteBit, v.Ticket)
			}
		}
	}
	//get remaining votes to be cast
	for _, v := range voteDetails.Vote.EligibleTickets {
		if !stringInSlice(v, allCastVotes) {
			ticketCont.TotalRemainTickets = append(ticketCont.TotalRemainTickets, v)
		}
	}
	ourTotal := len(ticketCont.OurAvailableToVoteTickets) + len(ticketCont.OurVotedTickets)
	notOurTotal := len(voteDetails.Vote.EligibleTickets) - ourTotal
	//get total counts
	ticketCont.TotalYes = len(ticketCont.TotalYesTickets)
	ticketCont.TotalNo = len(ticketCont.TotalYesTickets)
	ticketCont.TotalRemain = len(ticketCont.TotalRemainTickets)
	ticketCont.TotalVoted = len(ticketCont.TotalYesTickets) + len(ticketCont.TotalNoTickets)
	ticketCont.NotOurYes = len(ticketCont.NotOurVotedOnYesTickets)
	ticketCont.NotOurNo = len(ticketCont.NotOurVotedOnNoTickets)
	ticketCont.NotOurTotalVoted = len(ticketCont.NotOurVotedOnTickets)
	ticketCont.NotOurTotalRemain = notOurTotal - len(ticketCont.NotOurVotedOnTickets)
	ticketCont.OurYes = len(ticketCont.OurYesTickets)
	ticketCont.OurNo = len(ticketCont.OurNoTickets)
	ticketCont.OurAvailableToVote = len(ticketCont.OurAvailableToVoteTickets)
	ticketCont.OurTotalVoted = len(ticketCont.OurVotedTickets)
	ticketCont.OurTotal = ourTotal

	return
}

func (p *piv) fetchVoteResults(token string) (*tkv1.ResultsReply, error) {
	startTime := time.Now()

	r := tkv1.Results{
		Token: token,
	}
	responseBody, err := p.makeRequest(http.MethodPost,
		tkv1.APIRoute, tkv1.RouteResults, r)
	if err != nil {
		return nil, err
	}

	route := tkv1.APIRoute + tkv1.RouteResults
	fmt.Printf("%s request took %s\n", route, time.Since(startTime))

	var rr tkv1.ResultsReply
	err = json.Unmarshal(responseBody, &rr)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal ResultsReply: %v", err)
	}

	// Verify CastVoteDetails.
	version, err := p.getVersion()
	if err != nil {
		return nil, err
	}
	for _, cvd := range rr.Votes {
		err = client.CastVoteDetailsVerify(cvd, version.PubKey)
		if err != nil {
			return nil, err
		}
	}

	return &rr, nil
}

func (p *piv) fetchActiveProposals() ([]*tkv1.DetailsReply, error) {
	// fullRoute := pi.cfg.PoliteiaWWW + v1.PoliteiaWWWAPIRoute + v1.RouteActiveVote
	// fmt.Printf("%v...\n", fullRoute)
	version, err := p.getVersion()
	if err != nil {
		return nil, err
	}
	serverPubKey := version.PubKey
	page := uint32(1)
	var tokens []string
	for {
		ir, err := p._inventory(tkv1.Inventory{
			Page:   page,
			Status: tkv1.VoteStatusStarted,
		})
		if err != nil {
			return nil, err
		}

		pageTokens := ir.Vetted[tkv1.VoteStatuses[tkv1.VoteStatusStarted]]
		tokens = append(tokens, pageTokens...)
		if uint32(len(pageTokens)) < tkv1.InventoryPageSize {
			break
		}
		page++
	}

	voteDetailsReply := make([]*tkv1.DetailsReply, 0)
	for _, token := range tokens {
		// Get vote details.
		dr, err := p.voteDetails(token, serverPubKey)
		if err != nil {
			return nil, err
		}

		voteDetailsReply = append(voteDetailsReply, dr)
	}

	return voteDetailsReply, nil
}

func (p *piv) GetBestBlock() (int32, error) {
	bestBlockResponse, err := p.wallet.BestBlock(p.ctx, &pb.BestBlockRequest{})
	if err != nil {
		return -1, err
	}

	return int32(bestBlockResponse.Height), nil
}

// stringInSlice checks the slice `list` for `a` and
// returns true if exists.
func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}
