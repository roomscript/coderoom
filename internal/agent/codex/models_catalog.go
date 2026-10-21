package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/roomscript/coderoom/internal/agent"
)

type modelListing struct {
	models  []agent.ModelOption
	cursors map[string]bool
	done    bool
	err     error
}

// Models reads every catalog page without reserving the prompt gate.
func (c *Client) Models() error {
	c.turn.mu.Lock()
	if c.turn.threadID == "" {
		c.turn.mu.Unlock()
		return errors.New("codex: thread not started")
	}
	listing := &modelListing{cursors: make(map[string]bool)}
	c.models.listings = append(c.models.listings, listing)
	c.turn.mu.Unlock()
	if err := c.readModelPage(listing, ""); err != nil {
		c.turn.mu.Lock()
		c.models.listings = slices.DeleteFunc(c.models.listings, func(item *modelListing) bool { return item == listing })
		c.turn.mu.Unlock()
		return err
	}
	return nil
}

func (c *Client) readModelPage(listing *modelListing, cursor string) error {
	params := struct {
		Cursor string `json:"cursor,omitempty"`
	}{Cursor: cursor}
	return rpcWriteHandled(c, "model/list", params, func(raw json.RawMessage, err error) {
		if err != nil {
			c.finishModelListing(listing, err)
			return
		}
		models, next, err := parseModelPage(raw)
		if err != nil {
			c.finishModelListing(listing, err)
			return
		}
		listing.models = append(listing.models, models...)
		if next == "" {
			c.finishModelListing(listing, nil)
			return
		}
		if listing.cursors[next] {
			c.finishModelListing(listing, errors.New("codex: repeated model catalog cursor"))
			return
		}
		listing.cursors[next] = true
		if err := c.readModelPage(listing, next); err != nil {
			c.finishModelListing(listing, err)
		}
	})
}

func parseModelPage(raw json.RawMessage) ([]agent.ModelOption, string, error) {
	var page struct {
		Data []struct {
			Model   string `json:"model"`
			Efforts []struct {
				Effort string `json:"reasoningEffort"`
			} `json:"supportedReasoningEfforts"`
		} `json:"data"`
		Next string `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, "", fmt.Errorf("decode model catalog: %w", err)
	}
	if page.Data == nil {
		return nil, "", errors.New("codex: model catalog has no data array")
	}
	models := make([]agent.ModelOption, 0, len(page.Data))
	for _, entry := range page.Data {
		if entry.Model == "" {
			return nil, "", errors.New("codex: model catalog entry has no model")
		}
		opt := agent.ModelOption{Name: entry.Model, ID: entry.Model}
		for _, effort := range entry.Efforts {
			opt.Efforts = append(opt.Efforts, effort.Effort)
		}
		models = append(models, opt)
	}
	return models, page.Next, nil
}

func (c *Client) finishModelListing(listing *modelListing, err error) {
	c.turn.mu.Lock()
	listing.done, listing.err = true, err
	var outcomes []agent.ModelsListed
	for len(c.models.listings) > 0 && c.models.listings[0].done {
		head := c.models.listings[0]
		outcome := agent.ModelsListed{Models: head.models, Err: head.err}
		if head.err != nil {
			outcome.Models = nil
		}
		outcomes = append(outcomes, outcome)
		c.models.listings = c.models.listings[1:]
	}
	c.turn.mu.Unlock()
	for _, outcome := range outcomes {
		if !sendBufMessage(c.lifecycle.ctx, c, readMessage{msg: agent.Message{
			StreamID: modelsStreamID, Mode: agent.ModeSingle, Content: outcome,
		}}) {
			return
		}
	}
}
