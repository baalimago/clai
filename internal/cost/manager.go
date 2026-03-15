package cost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type ModelCatalogFetcher interface {
	FetchModel(ctx context.Context, model string) (ModelPriceScheme, error)
}

type Manager struct {
	fetcher        ModelCatalogFetcher
	debug          bool
	model          string
	configFilePath string
	price          *ModelPriceScheme
}

type Session interface {
	Start(ctx context.Context) <-chan error
	Enrich(chat pub_models.Chat) (pub_models.Chat, error)
}

func NewManager(fetcher ModelCatalogFetcher, model, configPath string) Manager {
	debug := misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("DEBUG_COST_MANAGER"))
	if debug {
		ancli.Noticef("setting up cost manager")
	}

	return Manager{
		fetcher:        fetcher,
		model:          model,
		configFilePath: configPath,
		debug:          debug,
	}
}

var errCacheMiss = errors.New("cache miss")

// storePriceScheme by updating the price field in m.configFilePath while keeping all other field as is
func (m *Manager) storePriceScheme(price ModelPriceScheme) error {
	if m.debug {
		ancli.Noticef("appending cache entry for: %v, price: %v", m.model, price)
	}

	b, err := os.ReadFile(m.configFilePath)
	if err != nil {
		return fmt.Errorf("read price config file %q: %w", m.configFilePath, err)
	}

	var config map[string]json.RawMessage
	if err := json.Unmarshal(b, &config); err != nil {
		return fmt.Errorf("unmarshal price config file %q: %w", m.configFilePath, err)
	}

	priceBytes, err := json.Marshal(price)
	if err != nil {
		return fmt.Errorf("marshal updated price for price config file %q: %w", m.configFilePath, err)
	}
	config["price"] = priceBytes

	updatedBytes, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal updated price config file %q: %w", m.configFilePath, err)
	}
	if err := os.WriteFile(m.configFilePath, updatedBytes, 0o644); err != nil {
		return fmt.Errorf("write updated price config file %q: %w", m.configFilePath, err)
	}

	return nil
}

// seekCached by checking the config item for a price item. If field 'price' doesnt exit
// its assumed to be a cache miss
func (m *Manager) seekCached() (ModelPriceScheme, error) {
	b, err := os.ReadFile(m.configFilePath)
	if err != nil {
		return ModelPriceScheme{}, fmt.Errorf(
			"read price config file %q: %w",
			m.configFilePath,
			err,
		)
	}

	var config map[string]json.RawMessage
	if err := json.Unmarshal(b, &config); err != nil {
		return ModelPriceScheme{}, fmt.Errorf(
			"unmarshal price config file %q: %w",
			m.configFilePath,
			err,
		)
	}

	priceRaw, ok := config["price"]
	if !ok {
		return ModelPriceScheme{}, errCacheMiss
	}

	var price ModelPriceScheme
	if err := json.Unmarshal(priceRaw, &price); err != nil {
		return ModelPriceScheme{}, fmt.Errorf(
			"unmarshal cached price for price config file %q: %w",
			m.configFilePath,
			err,
		)
	}

	return price, nil
}

func (m *Manager) resolveModelPrice(ctx context.Context) (ModelPriceScheme, error) {
	cached, err := m.seekCached()
	if err == nil {
		if m.debug {
			ancli.Noticef("cache hit for: %v", m.model)
		}
		return cached, nil
	}

	if err != nil && !errors.Is(err, errCacheMiss) {
		return ModelPriceScheme{}, fmt.Errorf("failed to find latest usable: %w", err)
	}

	if m.debug {
		ancli.Noticef("fetching model price for: %v", m.model)
	}
	price, err := m.fetcher.FetchModel(ctx, m.model)
	if err != nil {
		return ModelPriceScheme{}, fmt.Errorf("failed to fetch model: %w", err)
	}

	err = m.storePriceScheme(price)
	if err != nil {
		ancli.Errf("failed to store price scheme: %v", err)
	}
	return price, nil
}

func (m *Manager) Start(ctx context.Context) (<-chan struct{}, <-chan error) {
	errCh := make(chan error)
	readyCh := make(chan struct{})
	go func() {
		defer close(errCh)
		defer close(readyCh)

		if m.debug {
			ancli.Noticef("resolve routine started")
		}
		price, err := m.resolveModelPrice(ctx)
		if err != nil {
			select {
			case errCh <- err:
			default:
			}
			return
		}
		m.price = &price
		if m.debug {
			ancli.Noticef("resolve routine done, setting price to: %v", price)
		}
	}()
	return readyCh, errCh
}

func (m *Manager) Enrich(chat pub_models.Chat) (pub_models.Chat, error) {
	if m.debug {
		ancli.Noticef("encirchening (?) chat: %v, which has: %v queries", chat.ID, len(chat.Queries))
	}
	estimate, err := m.estimateUSD(chat.TokenUsage)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("enrich chat with cost estimate: %w", err)
	}
	_, idx, err := chat.LastOfRole("user")
	if err != nil {
		ancli.Warnf("failed to find user role: %v", err)
		idx = -1
	}
	chat.Queries = append(chat.Queries, pub_models.QueryCost{
		CreatedAt:      time.Now(),
		CostUSD:        estimate,
		MessageTrigger: idx,
		Model:          m.model,
		Usage:          *chat.TokenUsage,
	})

	if m.debug {
		ancli.Noticef("chat now has: %v query entries", len(chat.Queries))
	}
	return chat, nil
}
