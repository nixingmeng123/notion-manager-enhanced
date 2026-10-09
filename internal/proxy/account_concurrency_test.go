package proxy

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestAccountRuntimeStateConcurrentAccess(t *testing.T) {
	pool := NewAccountPool()
	acc := &Account{
		UserName:  "user",
		UserEmail: "user@example.com",
		PlanType:  "plus",
	}
	pool.accounts = []*Account{acc}

	initialCheckedAt := time.Unix(0, 0)
	acc.setModels([]ModelEntry{{Name: "GPT 5.4", ID: "model-0"}})
	acc.setQuotaInfo(&QuotaInfo{
		IsEligible:        true,
		SpaceUsage:        1,
		SpaceLimit:        100,
		UserUsage:         1,
		UserLimit:         50,
		ResearchModeUsage: 0,
		HasPremium:        true,
		PremiumBalance:    9,
		PremiumUsage:      1,
		PremiumLimit:      10,
	}, &initialCheckedAt)

	const iterations = 1000
	start := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			checkedAt := time.Unix(int64(i+1), 0)
			acc.setModels([]ModelEntry{
				{Name: fmt.Sprintf("GPT 5.%d", i%5), ID: fmt.Sprintf("model-%d", i%7)},
				{Name: fmt.Sprintf("Opus 4.%d", i%3), ID: fmt.Sprintf("opus-%d", i%5)},
			})
			acc.setQuotaInfo(&QuotaInfo{
				IsEligible:        i%4 != 0,
				SpaceUsage:        i % 100,
				SpaceLimit:        100,
				UserUsage:         i % 50,
				UserLimit:         50,
				ResearchModeUsage: i % 6,
				HasPremium:        i%2 == 0,
				PremiumBalance:    10 - (i % 10),
				PremiumUsage:      i % 10,
				PremiumLimit:      10,
			}, &checkedAt)
			if i%3 == 0 {
				acc.markQuotaExhausted(checkedAt, false)
			} else {
				acc.clearQuotaExhausted()
			}
		}
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				_ = pool.AllModels()
				_ = buildPublicModels(pool.AllModels())
				_ = pool.GetAccountDetails()
				_ = pool.GetQuotaSummary()
				_ = pool.AvailableCount()
				_ = pool.GetByEmail(acc.UserEmail)
				_ = isFreePlan(acc)
				_ = accountQuotaPriority(acc)
			}
		}()
	}

	close(start)
	wg.Wait()
}
