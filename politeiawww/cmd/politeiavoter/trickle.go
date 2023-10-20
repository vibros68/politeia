package main

import (
	"context"
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
	Vote tkv1.CastVote `json:"vote"` // RPC vote
	At   time.Time     `json:"at"`   // When initial vote will be submitted
}

func (p *piv) generateVoteAlarm(votesToCast []tkv1.CastVote, voteBitY, voteBitN string) ([]*voteAlarm, error) {
	voteDuration := p.cfg.voteDuration
	fmt.Printf("Total number of votes  : %v\n", len(votesToCast))
	fmt.Printf("Start time             : %s\n", viewTime(p.cfg.startTime))
	fmt.Printf("Vote duration          : %v\n", voteDuration)

	gaussion, err := NewGaussian(math.Sqrt(p.cfg.GaussianDerivation), 0, p.cfg.startTime, p.cfg.startTime.Add(voteDuration))
	if err != nil {
		return nil, err
	}

	va := make([]*voteAlarm, len(votesToCast))
	for k := range votesToCast {
		t, err := randomFutureTime(gaussion)
		if err != nil {
			return nil, err
		}
		va[k] = &voteAlarm{
			Vote: votesToCast[k],
			At:   t,
		}
	}

	return va, nil
}

func randomFutureTime(g *Gaussian) (time.Time, error) {
	now := time.Now()
	for {
		t, err := g.RandomTime()
		if err != nil {
			return time.Time{}, err
		}
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

func (p *piv) voteTicket(ectx context.Context, voteID int, va voteAlarm, voteBitY, voteBitN string) error {
	voteID++ // make human readable

	// Wait
	err := WaitUntil(ectx, va.At)
	if err != nil {
		return fmt.Errorf("%v vote %v failed: %v",
			viewTime(time.Now()), voteID, err)
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

func (p *piv) alarmTrickler(token string, votesToCast []tkv1.CastVote, voted int, voteBitY, voteBitN string) error {
	// Generate work queue
	votes, err := p.generateVoteAlarm(votesToCast, voteBitY, voteBitN)
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
