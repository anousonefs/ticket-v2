package bank

import (
	"context"
	"fmt"
	"ticket-system/internal/services/bank/jdb"
	"ticket-system/internal/services/bank/ldb"
)

// Factory implements BankFactory interface
type Factory struct{}

// NewFactory creates a new bank factory
func NewFactory() *Factory {
	return &Factory{}
}

// CreateBank creates a bank instance based on provider type and configuration
func (f *Factory) CreateBank(ctx context.Context, provider BankProvider, config interface{}) (BankInterface, error) {
	switch provider {
	case BankJDB:
		jdbConfig, ok := config.(*jdb.Config)
		if !ok {
			return nil, fmt.Errorf("invalid JDB config type, expected *jdb.Config")
		}
		return NewJDBAdapter(ctx, jdbConfig)

	case BankLDB:
		ldbConfig, ok := config.(*ldb.Config)
		if !ok {
			return nil, fmt.Errorf("invalid LDB config type, expected *ldb.Config")
		}
		return NewLDBAdapter(ctx, ldbConfig)

	case BankBCEL:
		// TODO: Implement BCEL adapter when BCEL client is available
		return nil, fmt.Errorf("BCEL bank provider not implemented yet")

	default:
		return nil, fmt.Errorf("unsupported bank provider: %s", provider)
	}
}

// GetSupportedProviders returns list of supported bank providers
func (f *Factory) GetSupportedProviders() []BankProvider {
	return []BankProvider{
		BankJDB,
		BankLDB,
		// services.BankBCEL, // TODO: Add when implemented
	}
}

// BankRegistry manages multiple bank instances
type BankRegistry struct {
	banks   map[BankProvider]BankInterface
	factory BankFactory
	primary BankProvider
}

// NewBankRegistry creates a new bank registry
func NewBankRegistry(factory BankFactory) *BankRegistry {
	return &BankRegistry{
		banks:   make(map[BankProvider]BankInterface),
		factory: factory,
	}
}

// RegisterBank registers a bank instance
func (r *BankRegistry) RegisterBank(ctx context.Context, provider BankProvider, config interface{}) error {
	bank, err := r.factory.CreateBank(ctx, provider, config)
	if err != nil {
		return fmt.Errorf("failed to create %s bank: %w", provider, err)
	}

	r.banks[provider] = bank

	// Set first registered bank as primary
	if r.primary == "" {
		r.primary = provider
	}

	return nil
}

// GetBank returns a bank instance by provider
func (r *BankRegistry) GetBank(provider BankProvider) (BankInterface, error) {
	bank, exists := r.banks[provider]
	if !exists {
		return nil, fmt.Errorf("bank provider %s not registered", provider)
	}
	return bank, nil
}

// GetPrimaryBank returns the primary bank instance
func (r *BankRegistry) GetPrimaryBank() (BankInterface, error) {
	if r.primary == "" {
		return nil, fmt.Errorf("no primary bank configured")
	}
	return r.GetBank(r.primary)
}

// SetPrimaryBank sets the primary bank provider
func (r *BankRegistry) SetPrimaryBank(provider BankProvider) error {
	if _, exists := r.banks[provider]; !exists {
		return fmt.Errorf("bank provider %s not registered", provider)
	}
	r.primary = provider
	return nil
}

// GetAvailableBanks returns list of registered bank providers
func (r *BankRegistry) GetAvailableBanks() []BankProvider {
	providers := make([]BankProvider, 0, len(r.banks))
	for provider := range r.banks {
		providers = append(providers, provider)
	}
	return providers
}

// Close gracefully closes all bank connections
func (r *BankRegistry) Close(ctx context.Context) error {
	for provider, bank := range r.banks {
		if err := bank.Close(ctx); err != nil {
			// Log error but continue closing other banks
			fmt.Printf("Error closing %s bank: %v\n", provider, err)
		}
	}
	return nil
}

