package text

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cost"
	"github.com/baalimago/clai/internal/models"
)

func writeModelPriceFixture(t *testing.T, conf Configurations, defaults any) {
	t.Helper()
	vendor, family, version, err := vendorType(conf.Model)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(defaults)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	config["model"] = conf.Model
	config["price"] = cost.ModelPriceScheme{InputUSDPerToken: 0.001, OutputUSDPerToken: 0.002}
	b, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	file := fmt.Sprintf("%s_%s_%s.json", vendor, family, strings.ReplaceAll(version, "/", "_"))
	if err := os.WriteFile(filepath.Join(conf.ConfigDir, file), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (q *Querier[C]) waitForFixtureCost(t *testing.T) {
	t.Helper()
	select {
	case <-q.costEnricher.ready:
	case <-time.After(time.Second):
		t.Fatal("fixture cost worker did not finish")
	}
}

func waitForQuerierCosts(t *testing.T, q models.Querier) {
	t.Helper()
	ready, ok := q.(interface{ waitForFixtureCost(*testing.T) })
	if !ok {
		t.Fatalf("querier %T does not expose fixture cost readiness", q)
	}
	ready.waitForFixtureCost(t)
}
