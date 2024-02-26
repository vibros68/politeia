package main

import (
	"context"
	pb "decred.org/dcrwallet/rpc/walletrpc"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"crypto/rand"

	tkv1 "github.com/decred/politeia/politeiawww/api/ticketvote/v1"
	"github.com/decred/politeia/util"
	"golang.org/x/sync/errgroup"
)

// WaitUntil will block until the given time.  Can be cancelled by cancelling
// the context
func WaitUntil(ctx context.Context, t time.Time) error {
	// This garbage is a fucking retarded lint idea.
	// We therefore replace the readable `diff := t.Sub(time.Now())` line
	// into unreadable time.Until() crap.
	diff := time.Until(t)
	if diff <= 0 {
		return nil
	}

	return WaitFor(ctx, diff)
}

// WaitFor will block for the specified duration or the context is cancelled
func WaitFor(ctx context.Context, diff time.Duration) error {
	timer := time.NewTimer(diff)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// voteAlarm represents a vote and the time at which it will be initially
// submitted to politeia.
type voteAlarm struct {
	Vote    tkv1.CastVote `json:"vote"` // RPC vote
	At      time.Time     `json:"at"`   // When initial vote will be submitted
	Address string        `json:"address"`
}

func (v *voteAlarm) Message() string {
	return v.Vote.Token + v.Vote.Ticket + v.Vote.VoteBit
}

type bunche struct {
	start time.Time
	end   time.Time
}

func (p *piv) randomVote(yesVotes, noVotes []*tkv1.CastVote) ([]*voteAlarm, error) {
	va := make([]*voteAlarm, len(yesVotes)+len(noVotes))
	var startTime = p.cfg.startTime
	var endTime = startTime.Add(p.cfg.voteDuration)
	for k := range yesVotes {
		t, err := randomFutureTime(startTime, endTime)
		if err != nil {
			return nil, err
		}

		va[k] = &voteAlarm{
			Vote: *yesVotes[k],
			At:   t,
		}
	}
	for k := range noVotes {
		t, err := randomFutureTime(startTime, endTime)
		if err != nil {
			return nil, err
		}

		va[k+len(yesVotes)] = &voteAlarm{
			Vote: *noVotes[k],
			At:   t,
		}
	}
	fmt.Printf("Voting [%d] vote yes, [%d] vote no", len(yesVotes), len(noVotes))
	return va, nil
}

func (p *piv) batchesVoteAlarm(yesVotes, noVotes []*voteAlarm) ([]*voteAlarm, error) {
	bunchesLen := int(p.cfg.Bunches)
	bunches := make([]bunche, bunchesLen)
	voteDuration := p.cfg.voteDuration
	var total = len(yesVotes) + len(noVotes)
	fmt.Printf("votes %d  bunches %d  duration %s \n", total, len(bunches), voteDuration)
	fmt.Printf("start: %s end: %s \n", viewTime(p.cfg.startTime), viewTime(p.cfg.endTime))

	for i := 0; i < int(p.cfg.Bunches); i++ {
		start, end, err := randomTime(voteDuration, p.cfg.startTime)
		if err != nil {
			return nil, err
		}
		b := bunche{
			start: start,
			end:   end,
		}
		bunches[i] = b
		fmt.Printf("bunchID: %v start %v end %v duration %v\n",
			i, viewTime(start), viewTime(end), end.Sub(start))
	}
	var batchesYes, batchesNo int
	if len(yesVotes) == 0 {
		batchesNo = bunchesLen
	}
	if len(noVotes) == 0 {
		batchesYes = bunchesLen
	}
	if len(yesVotes) > 0 && len(noVotes) > 0 {
		batchesNo = bunchesLen / 2
		batchesYes = bunchesLen - batchesNo
	}

	if p.cfg.isMirror {
		fmt.Printf("votes: %d  bunches: %d \n",
			len(yesVotes), batchesYes)
	} else {
		fmt.Printf("votes: yes %d no %d  bunches: yes %d no  %d \n",
			len(yesVotes), len(noVotes), batchesYes, batchesNo)
	}

	timeFrame := voteDuration / time.Duration(p.cfg.ChartCols)
	var yesChartConf = make([]int, p.cfg.ChartCols)
	var noChartConf = make([]int, p.cfg.ChartCols)
	va := make([]*voteAlarm, len(yesVotes)+len(noVotes))
	for k := range yesVotes {
		i := k % batchesYes
		t, err := randomFutureTime(bunches[i].start, bunches[i].end)
		if err != nil {
			return nil, err
		}
		timeDiff := t.Sub(p.cfg.startTime)
		index := timeDiff / timeFrame
		yesChartConf[index] = yesChartConf[index] + 1
		yesVotes[k].At = t
		va[k] = yesVotes[k]
	}
	for k := range noVotes {
		i := k % batchesNo
		t, err := randomFutureTime(bunches[i+batchesYes].start, bunches[i+batchesYes].end)
		if err != nil {
			return nil, err
		}
		timeDiff := t.Sub(p.cfg.startTime)
		index := timeDiff / timeFrame
		noChartConf[index] = noChartConf[index] + 1

		noVotes[k].At = t
		va[k+len(yesVotes)] = noVotes[k]
	}
	if p.cfg.isMirror {
		fmt.Printf("votes chart: bunches %d, largest %v *scaled to %v rows \n", batchesYes, findMax(yesChartConf), p.cfg.ChartRows)
		displayChart(yesChartConf, p.cfg.ChartRows)
	} else {
		fmt.Printf("yes chart: bunches %d, largest %v *scaled to %v rows \n", batchesYes, findMax(yesChartConf), p.cfg.ChartRows)
		displayChart(yesChartConf, p.cfg.ChartRows)
		fmt.Printf("no chart: bunches %d, largest %v *scaled to %v rows \n", batchesNo, findMax(noChartConf), p.cfg.ChartRows)
		displayChart(noChartConf, p.cfg.ChartRows)
	}

	return va, nil
}

func (p *piv) gaussianVoteAlarm(votesToCast []*voteAlarm) ([]*voteAlarm, error) {
	voteDuration := p.cfg.voteDuration
	fmt.Printf("Total number of votes  : %v\n", len(votesToCast))
	fmt.Printf("Start time             : %s\n", viewTime(p.cfg.startTime))
	fmt.Printf("Vote duration          : %v\n", voteDuration)
	g, err := NewGaussian(math.Sqrt(p.cfg.GaussianDeviate), 0, p.cfg.startTime, p.cfg.endTime, p.cfg.ChartCols)
	if err != nil {
		return nil, err
	}
	va, err := g.GenerateTime(votesToCast, time.Now())
	if err != nil {
		return nil, err
	}
	if p.cfg.isMirror {
		fmt.Printf("vote chart, largest %v *scaled to %v rows \n", findMax(g.YesTimeGraph), p.cfg.ChartRows)
		displayChart(g.YesTimeGraph, p.cfg.ChartRows)
	} else {
		fmt.Printf("Yes vote chart, largest %v *scaled to %v rows \n", findMax(g.YesTimeGraph), p.cfg.ChartRows)
		displayChart(g.YesTimeGraph, p.cfg.ChartRows)
		fmt.Printf("No vote chart, largest %v *scaled to %v rows \n", findMax(g.NoTimeGraph), p.cfg.ChartRows)
		displayChart(g.NoTimeGraph, p.cfg.ChartRows)
	}
	return va, nil
}

func randomFutureTime(startTime, endTime time.Time) (time.Time, error) {
	now := time.Now()
	start := new(big.Int).SetInt64(startTime.Unix())
	end := new(big.Int).SetInt64(endTime.Unix())
	// Generate random time to fire off vote

	for {
		r, err := rand.Int(rand.Reader, new(big.Int).Sub(end, start))
		if err != nil {
			return time.Time{}, err
		}
		t := time.Unix(startTime.Unix()+r.Int64(), 0)
		if t.Unix() > now.Unix() {
			return t, nil
		}
	}
}

// randomDuration returns a randomly selected Duration between the provided
// min and max (in seconds).
func randomDuration(min, max byte) time.Duration {
	var (
		wait []byte
		err  error
	)
	for {
		wait, err = util.Random(1)
		if err != nil {
			// This really shouldn't happen so just use min seconds
			wait = []byte{min}
		} else {
			if wait[0] < min || wait[0] > max {
				continue
			}
			//fmt.Printf("min %v max %v got %v\n", min, max, wait[0])
		}
		break
	}
	return time.Duration(wait[0]) * time.Second
}

func randomTime(d time.Duration, startPoint time.Time) (time.Time, time.Time, error) {
	halfDuration := int64(d / 2)
	st, err := randomInt64(0, halfDuration*90/100) // up to 90% of half
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	endDuration := int64(time.Since(startPoint))
	if endDuration < halfDuration {
		endDuration = halfDuration
		if endDuration >= int64(d) {
			return time.Time{}, time.Time{}, fmt.Errorf("vote time is ended, it looks impossible")
		}
	}

	et, err := randomInt64(endDuration, int64(d))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	startTime := startPoint.Add(time.Duration(st)).Unix()
	endTime := startPoint.Add(time.Duration(et)).Unix()
	return time.Unix(startTime, 0), time.Unix(endTime, 0), nil
}

func (p *piv) voteTicket(ectx context.Context, voteID int, va voteAlarm, voteBitY, voteBitN string) error {
	voteID++ // make human readable
	if p.cfg.EmulateVote > 0 {
		p.ballotResults = append(p.ballotResults, tkv1.CastVoteReply{
			Ticket:       va.Vote.Ticket,
			Receipt:      "",
			ErrorCode:    nil,
			ErrorContext: "",
		})
		if va.Vote.VoteBit == voteBitY {
			p.votedYes++
		} else {
			p.votedNo++
		}
		return nil
	}

	// Wait
	err := WaitUntil(ectx, va.At)
	if err != nil {
		return fmt.Errorf("%v vote %v failed: %v",
			viewTime(time.Now()), voteID, err)
	}
	if p.cfg.isMirror {
		va.Vote.VoteBit = p.mc.getVoteBit()
		// re-sign vote for mirror
		passphrase, _ := p.walletPassphrase()
		sm := &pb.SignMessagesRequest{
			Passphrase: passphrase,
			Messages: []*pb.SignMessagesRequest_Message{
				{
					Address: va.Address,
					Message: va.Message(),
				},
			},
		}
		smr, err := p.wallet.SignMessages(p.ctx, sm)
		if err != nil {
			return err
		}
		if len(smr.Replies) == 0 {
			return fmt.Errorf("sign vote failed")
		}
		va.Vote.Signature = hex.EncodeToString(smr.Replies[0].Signature)
	}
	var voteSide = "yes"
	if va.Vote.VoteBit == voteBitN {
		voteSide = "no"
	}

	// Vote
	for retry := 0; ; retry++ {
		var rmsg string
		if retry != 0 {
			// Wait between 1 and 17 seconds
			d := randomDuration(3, 17)
			rmsg = fmt.Sprintf("retry %v (%v) ", retry, d)
			err = WaitFor(ectx, d)
			if err != nil {
				return fmt.Errorf("%v vote %v(%s) failed: %v",
					viewTime(time.Now()), voteID, voteSide, err)
			}
		}

		fmt.Printf("%v voting vote %v(%s) %v%v\n",
			viewTime(time.Now()), voteID, voteSide, rmsg, va.Vote.Ticket)

		// Send off vote
		b := tkv1.CastBallot{Votes: []tkv1.CastVote{va.Vote}}
		vr, err := p.sendVote(&b)
		var e ErrRetry
		if errors.As(err, &e) {
			// Append failed vote to retry queue
			fmt.Printf("Vote rescheduled: %v\n", va.Vote.Ticket)
			err := p.jsonLog(failedJournal, va.Vote.Token, b, e)
			if err != nil {
				return fmt.Errorf("0 jsonLog: %v", err)
			}

			// Retry
			continue

		} else if err != nil {
			// Unrecoverable error
			return fmt.Errorf("unrecoverable error: %v",
				err)
		}

		// Evaluate errors when ErrorCode is set
		if vr.ErrorCode != nil {
			switch *vr.ErrorCode {
			// Silently ignore.
			case tkv1.VoteErrorTicketAlreadyVoted:
				// This happens during network errors. Since
				// the ticket has already voted record success
				// and exit.

			// Restart
			case tkv1.VoteErrorInternalError:
				// Politeia puked. Retry later to see if it
				// recovered.
				continue

			// Non-terminal errors
			case tkv1.VoteErrorTokenInvalid,
				tkv1.VoteErrorRecordNotFound,
				tkv1.VoteErrorMultipleRecordVotes,
				tkv1.VoteErrorVoteBitInvalid,
				tkv1.VoteErrorSignatureInvalid,
				tkv1.VoteErrorTicketNotEligible:

				// Log failure
				err = p.jsonLog(failedJournal, va.Vote.Token, vr)
				if err != nil {
					return fmt.Errorf("1 jsonLog: %v", err)
				}

				// We have to do this for all failures, this
				// should be rewritten.
				p.Lock()
				p.ballotResults = append(p.ballotResults, *vr)
				p.Unlock()

				return nil

			// Terminal
			case tkv1.VoteErrorVoteStatusInvalid:
				// Force an exit of the both the main queue and the
				// retry queue if the voting period has ended.
				err = p.jsonLog(failedJournal, va.Vote.Token, vr)
				if err != nil {
					return fmt.Errorf("2 jsonLog: %v", err)
				}
				return fmt.Errorf("Vote has ended; forced " +
					"exit main vote queue.")

			// Should not happen
			default:
				// Log failure
				err = p.jsonLog(failedJournal, va.Vote.Token, vr)
				if err != nil {
					return fmt.Errorf("3 jsonLog: %v", err)
				}

				// We have to do this for all failures, this
				// should be rewritten.
				p.Lock()
				p.ballotResults = append(p.ballotResults, *vr)
				p.Unlock()

				return nil
			}
		}

		// Success, log it and exit
		p.mc.updateVoteBit(va.Vote.VoteBit)
		err = p.jsonLog(successJournal, va.Vote.Token, vr)
		if err != nil {
			return fmt.Errorf("3 jsonLog: %v", err)
		}

		// All done with this vote
		// Vote completed
		p.Lock()
		p.ballotResults = append(p.ballotResults, *vr)
		if va.Vote.VoteBit == voteBitY {
			p.votedYes++
		} else {
			p.votedNo++
		}
		// This is required to be in the lock to prevent a
		// ballotResults race
		fmt.Printf("%v finished vote %v(%s) -- total progress %v/%v\n",
			viewTime(time.Now()), voteID, voteSide, len(p.ballotResults), cap(p.ballotResults))
		p.Unlock()

		return nil
	}

	// Not reached
}

func randomInt64(min, max int64) (int64, error) {
	mi := new(big.Int).SetInt64(min)
	ma := new(big.Int).SetInt64(max)
	r, err := rand.Int(rand.Reader, new(big.Int).Sub(ma, mi))
	if err != nil {
		return 0, err
	}
	return new(big.Int).Add(mi, r).Int64(), nil
}

func (p *piv) alarmTrickler(token string, votesToCast, yesVotes, noVotes []*voteAlarm, voteBitY, voteBitN string) error {
	// Generate work queue
	var votes []*voteAlarm
	var err error
	if p.cfg.Gaussian {
		votes, err = p.gaussianVoteAlarm(votesToCast)
	} else {
		votes, err = p.batchesVoteAlarm(yesVotes, noVotes)
	}
	if p.cfg.EmulateVote > 0 {
		fmt.Printf("We are at emulation mode and will stop the process here. all votes assump to be success\n")
		return nil
	}

	if p.cfg.IntervalStatsTable > 0 /* && p.cfg.EmulateVote == 0*/ {
		go p.statsTableInterval()
	}
	if err != nil {
		return err
	}
	// Log work
	err = p.jsonLog(workJournal, token, votes)
	if err != nil {
		return err
	}

	// Launch the voting stats handler
	go p.statsHandler()

	// Launch voting go routines
	eg, ectx := errgroup.WithContext(p.ctx)
	p.ballotResults = make([]tkv1.CastVoteReply, 0, len(votesToCast))
	for k := range votes {
		voterID := k
		v := *votes[k]

		// Calculate of

		eg.Go(func() error {
			return p.voteTicket(ectx, voterID, v, voteBitY, voteBitN)
		})
	}
	err = eg.Wait()
	if err != nil {
		//fmt.Printf("%v\n", err)
		return err
	}

	return nil
}

func (p *piv) statsTableInterval() {
	for {
		p.tallyTable(p.args)
		time.Sleep(time.Minute * time.Duration(p.cfg.IntervalStatsTable))
	}
}
