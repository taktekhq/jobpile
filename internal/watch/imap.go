package watch

import (
	"context"
	"fmt"
	"strings"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// previewFetchSize caps how many raw bytes of a text part are pulled
// over IMAP before decoding and truncating to a preview: generous
// enough for any real signature or short reply, small enough to never
// pull down a large attachment by accident.
const previewFetchSize = 20000

// Candidate is one message pulled from the mailbox: enough to match
// and notify on.
type Candidate struct {
	MessageID string
	From      string // bare address, e.g. user@example.com
	FromName  string // display name; empty when the sender set none
	Subject   string
	Date      time.Time
	Preview   string // the text part's content, not yet collapsed or truncated
}

// FetchCandidates connects to addr (host:port) over TLS, logs in,
// selects mailbox, and returns every message since 'since'. It never
// marks a message as read: every body fetch uses BODY.PEEK, and
// nothing is ever written back to the server.
func FetchCandidates(ctx context.Context, addr, user, password, mailbox string, since time.Time) ([]Candidate, error) {
	c, err := imapclient.DialTLS(addr, nil)
	if err != nil {
		return nil, fmt.Errorf("watch: connecting to %s: %w", addr, err)
	}
	defer c.Close()

	if err := c.Login(user, password).Wait(); err != nil {
		return nil, fmt.Errorf("watch: login: %w", err)
	}
	defer c.Logout()

	if _, err := c.Select(mailbox, nil).Wait(); err != nil {
		return nil, fmt.Errorf("watch: selecting mailbox %q: %w", mailbox, err)
	}

	searchData, err := c.UIDSearch(&imap.SearchCriteria{Since: since}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("watch: search: %w", err)
	}
	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}

	fetchCmd := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		Envelope:      true,
		BodyStructure: &imap.FetchItemBodyStructure{},
		UID:           true,
	})
	msgs, err := fetchCmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("watch: fetching headers: %w", err)
	}

	candidates := make([]Candidate, 0, len(msgs))
	for _, m := range msgs {
		cand := Candidate{}
		if m.Envelope != nil {
			cand.Subject = m.Envelope.Subject
			cand.Date = m.Envelope.Date
			cand.MessageID = m.Envelope.MessageID
			if len(m.Envelope.From) > 0 {
				cand.From = m.Envelope.From[0].Addr()
				cand.FromName = m.Envelope.From[0].Name
			}
		}
		if cand.MessageID == "" {
			// Some senders omit Message-ID; fall back to a UID-based
			// key so seen-state still dedupes within this mailbox
			// across runs (the UID itself does not change once
			// assigned).
			cand.MessageID = fmt.Sprintf("uid:%d", m.UID)
		}

		if path, sp, ok := FindTextPart(m.BodyStructure); ok {
			raw, ferr := fetchBodySection(c, m.UID, path)
			if ferr == nil {
				cand.Preview = DecodePart(sp, raw)
			}
		}

		candidates = append(candidates, cand)
	}
	return candidates, nil
}

// fetchBodySection fetches one BODY[] section for a single message by
// UID, with BODY.PEEK so the message is never marked as read.
func fetchBodySection(c *imapclient.Client, uid imap.UID, path []int) ([]byte, error) {
	section := &imap.FetchItemBodySection{
		Part: path,
		Peek: true,
		Partial: &imap.SectionPartial{
			Offset: 0,
			Size:   previewFetchSize,
		},
	}
	fetchCmd := c.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{section},
	})
	msgs, err := fetchCmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("watch: fetching body for uid %d: %w", uid, err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("watch: no fetch data for uid %d", uid)
	}
	raw := msgs[0].FindBodySection(section)
	if raw == nil {
		return nil, fmt.Errorf("watch: body section missing for uid %d", uid)
	}
	return raw, nil
}

// AddrOrPort appends the default IMAP-over-TLS port when host has no
// port of its own, so config.toml only needs a bare hostname in the
// common case.
func AddrOrPort(host string) string {
	if strings.Contains(host, ":") {
		return host
	}
	return host + ":993"
}
