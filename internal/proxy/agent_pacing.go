package proxy

import (
	"context"
	"time"
)

// Pace creation per account. Waiting callers can cancel without reserving
// future slots or holding account/config locks. This is a configurable local
// policy, not an assertion about Notion's undocumented rate limit.
func waitAgentRequestSlot(ctx context.Context, acc *Account) error {
	interval := time.Duration(enhancedConfigSnapshot().MinRequestIntervalSeconds) * time.Second
	if interval > 0 {
		acc.mu.Lock()
		if acc.agentRequestGate == nil {
			acc.agentRequestGate = make(chan struct{}, 1)
		}
		gate := acc.agentRequestGate
		acc.mu.Unlock()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return ctx.Err()
		}
		delay := time.Until(acc.agentSnapshot().LastStartedAt.Add(interval))
		if delay > interval {
			delay = interval // tolerate a wall-clock correction after restart
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	acc.updateAgentCapability(func(c *AgentCapability) { c.LastStartedAt = time.Now() })
	return nil
}
