package agent

import (
	"context"
	"errors"
	"testing"

	privmodels "github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/claierr"
	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/clai/pkg/text/models"
)

type compatibilityQuerier struct{ err error }

func (q compatibilityQuerier) Query(context.Context) error { return q.err }
func (q compatibilityQuerier) TextQuery(context.Context, models.Chat) (models.Chat, error) {
	return models.Chat{}, q.err
}

// TestAgentErrorCompatibility proves Unwrap.1 and F2 for existing agent failures.
func TestAgentErrorCompatibility(t *testing.T) {
	t.Parallel()

	cause := errors.New("transport failed")
	typed := claierr.NewTransport(cause)

	t.Run("query", func(t *testing.T) {
		agent := Agent{querier: compatibilityQuerier{err: typed}}
		_, err := agent.Query(context.Background(), models.Chat{})
		assertCompatibleAgentError(t, err, typed, cause, "Agent.TextQuery: transport failure: transport failed")
	})

	t.Run("setup", func(t *testing.T) {
		agent := New(WithConfigDir(t.TempDir()))
		agent.querierCreator = func(context.Context, text.Configurations) (privmodels.Querier, error) {
			return nil, typed
		}
		err := agent.Setup(context.Background())
		assertCompatibleAgentError(t, err, typed, cause, "publicQuerier.Setup failed to CreateTextQuerier: transport failure: transport failed")
	})
}

func assertCompatibleAgentError(t *testing.T, err, typed, cause error, wantText string) {
	t.Helper()
	if err == nil {
		t.Fatal("Unwrap.1: expected an error")
	}
	if got := err.Error(); got != wantText {
		t.Fatalf("F2: error text = %q, want %q", got, wantText)
	}
	if !errors.Is(err, cause) {
		t.Fatal("Unwrap.1: errors.Is cannot reach original cause")
	}
	var transport *claierr.TransportError
	if !errors.As(err, &transport) || transport != typed {
		t.Fatal("F2: errors.As cannot reach existing typed error")
	}
	if _, ok := errors.AsType[diagnostic.Locator](err); !ok {
		t.Fatal("Location.1: agent context does not implement Locator")
	}
}
