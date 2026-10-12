package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestAgentPacingPersistsAndCancelsWithoutReservingSlots(t *testing.T) {
	previous := AppConfig
	AppConfig = DefaultConfig()
	AppConfig.AgentV3.MinRequestIntervalSeconds = 1
	t.Cleanup(func() { AppConfig = previous })
	acc := &Account{}
	if err := waitAgentRequestSlot(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	started := acc.agentSnapshot().LastStartedAt
	data, _ := json.Marshal(acc)
	var restored Account
	if json.Unmarshal(data, &restored) != nil || restored.agentSnapshot().LastStartedAt.IsZero() {
		t.Fatal("last start was lost on reload")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	before := time.Now()
	if err := waitAgentRequestSlot(ctx, &restored); !errors.Is(err, context.DeadlineExceeded) || time.Since(before) > time.Second {
		t.Fatalf("pacing cancellation failed: %v", err)
	}
	if !restored.agentSnapshot().LastStartedAt.Equal(started) {
		t.Fatal("cancelled waiter reserved another slot")
	}
	restored.updateAgentCapability(func(c *AgentCapability) { c.LastStartedAt = time.Now().Add(-2 * time.Second) })
	if err := waitAgentRequestSlot(context.Background(), &restored); err != nil {
		t.Fatal("cancelled waiter held the gate")
	}
}

func TestAgentPacingCancelsWhileAnotherWaiterHoldsGate(t *testing.T) {
	previous := AppConfig
	AppConfig = DefaultConfig()
	AppConfig.AgentV3.MinRequestIntervalSeconds = 1
	t.Cleanup(func() { AppConfig = previous })
	acc := &Account{agentRequestGate: make(chan struct{}, 1)}
	acc.agentRequestGate <- struct{}{}
	defer func() { <-acc.agentRequestGate }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := waitAgentRequestSlot(ctx, acc); !errors.Is(err, context.DeadlineExceeded) || !acc.agentSnapshot().LastStartedAt.IsZero() {
		t.Fatalf("blocked waiter did not cancel cleanly: %v", err)
	}
}
