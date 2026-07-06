package bedrock

import (
	"testing"

	"oss.nandlabs.io/golly/genai"
)

// newTestProvider returns a BedrockProvider suitable for catalog tests. The
// catalog methods do not touch the AWS client, so a zero-value provider is fine.
func newTestProvider() *BedrockProvider {
	return &BedrockProvider{}
}

func TestModelCatalog_NonEmpty(t *testing.T) {
	p := newTestProvider()
	catalog := p.ModelCatalog()
	if len(catalog) == 0 {
		t.Fatal("ModelCatalog() returned an empty catalog")
	}
	for i, m := range catalog {
		if m.Name == "" {
			t.Errorf("catalog[%d]: empty Name", i)
		}
		if m.Provider != p.Name() {
			t.Errorf("catalog[%d] (%s): Provider = %q, want %q", i, m.Name, m.Provider, p.Name())
		}
		if len(m.Capabilities) == 0 {
			t.Errorf("catalog[%d] (%s): no capabilities", i, m.Name)
		}
	}
}

func TestModelCatalog_ReturnsCopy(t *testing.T) {
	p := newTestProvider()

	first := p.ModelCatalog()
	if len(first) == 0 {
		t.Fatal("empty catalog")
	}

	// Mutate the returned slice and the reference fields of the first entry.
	origName := first[0].Name
	origCapLen := len(first[0].Capabilities)
	first[0].Name = "MUTATED"
	if len(first[0].Capabilities) > 0 {
		first[0].Capabilities[0] = "MUTATED_CAP"
	}
	first[0].Metadata["family"] = "MUTATED_FAMILY"

	second := p.ModelCatalog()
	if second[0].Name != origName {
		t.Errorf("mutating returned slice leaked into package table: got Name %q, want %q", second[0].Name, origName)
	}
	if len(second[0].Capabilities) != origCapLen || second[0].Capabilities[0] == "MUTATED_CAP" {
		t.Error("mutating returned Capabilities slice leaked into package table")
	}
	if second[0].Metadata["family"] == "MUTATED_FAMILY" {
		t.Error("mutating returned Metadata map leaked into package table")
	}
}

func TestModelInfoFor_Known(t *testing.T) {
	p := newTestProvider()

	const id = "anthropic.claude-3-5-sonnet-20241022-v2:0"
	info, ok := p.ModelInfoFor(id)
	if !ok {
		t.Fatalf("ModelInfoFor(%q) = false, want true", id)
	}
	if info.Name != id {
		t.Errorf("Name = %q, want %q", info.Name, id)
	}
	if !info.Has(genai.CapVision) {
		t.Errorf("%s should advertise CapVision", id)
	}
	if !info.Has(genai.CapToolCalling) {
		t.Errorf("%s should advertise CapToolCalling", id)
	}
}

func TestModelInfoFor_Unknown(t *testing.T) {
	p := newTestProvider()
	if _, ok := p.ModelInfoFor("does.not.exist-v9:9"); ok {
		t.Error("ModelInfoFor(unknown) = true, want false")
	}
}

func TestEmbeddingModels_OnlyEmbeddings(t *testing.T) {
	p := newTestProvider()
	embedIDs := []string{
		"amazon.titan-embed-text-v2:0",
		"amazon.titan-embed-text-v1",
		"cohere.embed-english-v3",
		"cohere.embed-multilingual-v3",
	}
	for _, id := range embedIDs {
		info, ok := p.ModelInfoFor(id)
		if !ok {
			t.Errorf("embed model %q not found in catalog", id)
			continue
		}
		if !info.Has(genai.CapEmbeddings) {
			t.Errorf("%s: missing CapEmbeddings", id)
		}
		if info.Has(genai.CapText) {
			t.Errorf("%s: unexpectedly advertises CapText", id)
		}
		if info.Has(genai.CapChat) {
			t.Errorf("%s: unexpectedly advertises CapChat", id)
		}
	}
}

func TestCapabilityProvider_Conformance(t *testing.T) {
	p := newTestProvider()
	// The var _ assertion in models.go guarantees this at compile time; assert it
	// at runtime through the genai.Provider seam the router uses for discovery.
	if _, ok := genai.Provider(p).(genai.CapabilityProvider); !ok {
		t.Fatal("*BedrockProvider does not satisfy genai.CapabilityProvider via genai.Provider")
	}
}

// TestRouterIntegration_CapabilityRouting proves the catalog wires end-to-end
// into the real golly v1.8.0 model router: the registry surfaces the catalog and
// a CapabilityStrategy ranking a vision task selects a vision-capable Bedrock
// model.
func TestRouterIntegration_CapabilityRouting(t *testing.T) {
	p := newTestProvider()

	set := genai.NewProviderSet(p)
	registry, err := genai.NewModelRegistry(set, genai.RoutingConfig{})
	if err != nil {
		t.Fatalf("NewModelRegistry: %v", err)
	}

	all := registry.All()
	if len(all) != len(bedrockCatalog) {
		t.Fatalf("registry.All() has %d models, want %d (catalog)", len(all), len(bedrockCatalog))
	}

	// Every catalog model must be reachable by its registry key.
	for _, m := range bedrockCatalog {
		if _, ok := registry.Get(p.Name(), m.Name); !ok {
			t.Errorf("registry missing %s/%s", p.Name(), m.Name)
		}
	}

	strategy := genai.NewCapabilityStrategy()
	task := genai.Task{
		Type:                 genai.TaskChat,
		RequiredCapabilities: []genai.Capability{genai.CapVision},
	}
	ranked := strategy.Rank(task, registry)
	if len(ranked) == 0 {
		t.Fatal("CapabilityStrategy returned no candidates for a vision task")
	}
	for _, c := range ranked {
		info, ok := registry.Get(c.Provider, c.Model)
		if !ok {
			t.Fatalf("ranked candidate %s/%s not in registry", c.Provider, c.Model)
		}
		if !info.Has(genai.CapVision) {
			t.Errorf("ranked candidate %s lacks CapVision", c.Model)
		}
	}
	// At least one Bedrock vision model must have survived.
	if ranked[0].Provider != p.Name() {
		t.Errorf("top candidate provider = %q, want %q", ranked[0].Provider, p.Name())
	}
}
