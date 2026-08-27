package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SUSE/aif-operator/internal/catalog"
)

const (
	multimodalSafetyNIMSlug = "multimodal-safety-nim"
	deepstreamITSSlug       = "deepstream-its"
	gpuOperatorSlug         = "gpu-operator"
	nvidiaHelmRepo          = "https://helm.ngc.nvidia.com/nvidia"
	nimHelmRepo             = "https://helm.ngc.nvidia.com/nim/nvidia"
	nemoHelmRepo            = "https://helm.ngc.nvidia.com/nvidia/nemo-microservices"
)

// baseCatalog holds one suse-ai entry (Milvus), one generator-owned nvidia entry
// (gpu-operator, carrying the Supported chip), and one hand-added unowned nvidia
// entry (deepstream-its, no label).
const baseCatalog = `{
  "suse-ai": [
    {
      "name":"Milvus",
      "slug_name":"milvus",
      "packaging_format":"HELM_CHART",
      "repository_url":"oci://dp.apps.rancher.io/charts",
      "labels":[{"code":"supported","name":"Supported"}]
    }
  ],
  "nvidia": [
    {
      "name":"GPU Operator",
      "slug_name":"gpu-operator",
      "packaging_format":"HELM_CHART",
      "repository_url":"https://helm.ngc.nvidia.com/nvidia",
      "labels":[{"code":"supported","name":"Supported"}]
    },
    {
      "name":"DeepStream ITS",
      "slug_name":"deepstream-its",
      "packaging_format":"HELM_CHART",
      "repository_url":"https://helm.ngc.nvidia.com/nvidia"
    }
  ]
}`

// findNVIDIA returns the single nvidia entry with the given slug, or nil.
func findNVIDIA(doc catalogDoc, slug string) *catalog.Item {
	for i := range doc.NVIDIA {
		if doc.NVIDIA[i].SlugName == slug {
			return &doc.NVIDIA[i]
		}
	}
	return nil
}

func mustOverrides(t *testing.T, s string) overrides {
	t.Helper()
	ov, err := loadOverrides([]byte(s))
	if err != nil {
		t.Fatalf("loadOverrides: %v", err)
	}
	return ov
}

func TestSyncNVAIE_AddsNewEntry(t *testing.T) {
	derived := []catalog.Item{
		// Keep gpu-operator so it isn't dropped as stale; add a new chart.
		{Name: "GPU Operator", SlugName: gpuOperatorSlug, PackagingFormat: helmChart, RepositoryURL: nvidiaHelmRepo},
		{
			Name: "Multimodal Safety NIM", SlugName: multimodalSafetyNIMSlug,
			PackagingFormat: helmChart, RepositoryURL: nimHelmRepo,
		},
	}
	out, added, removed, err := syncNVAIE([]byte(baseCatalog), derived, overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != multimodalSafetyNIMSlug {
		t.Fatalf("added = %v", added)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v", removed)
	}
	var doc catalogDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	found := findNVIDIA(doc, multimodalSafetyNIMSlug)
	if found == nil {
		t.Fatal("new entry not added")
	}
	if len(found.Labels) != 1 || found.Labels[0] != (catalog.Label{Code: supportedCode, Name: "Supported"}) {
		t.Fatalf("new entry label = %+v", found.Labels)
	}
	// suse-ai untouched and still first in output.
	if len(doc.SuseAI) != 1 || doc.SuseAI[0].SlugName != "milvus" {
		t.Fatalf("suse-ai changed: %+v", doc.SuseAI)
	}
	if strings.Index(string(out), `"suse-ai"`) > strings.Index(string(out), `"nvidia"`) {
		t.Fatal("top-level key order must stay suse-ai then nvidia")
	}
}

func TestSyncNVAIE_RefreshesOwnedFieldsFromNGC(t *testing.T) {
	// An owned entry (gpu-operator) is rebuilt from derived: fresh NGC fields
	// overwrite the stored ones. This is the inverse of the old preserve behavior.
	derived := []catalog.Item{{
		Name: "GPU Operator (renamed)", SlugName: gpuOperatorSlug,
		Description: "fresh description", PackagingFormat: helmChart, RepositoryURL: nvidiaHelmRepo,
	}}
	out, added, removed, err := syncNVAIE([]byte(baseCatalog), derived, overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Fatalf("owned refresh must not be counted as added: %v", added)
	}
	// deepstream-its is unowned and untouched; gpu-operator is the only owned entry
	// and is present in derived, so nothing is removed.
	if len(removed) != 0 {
		t.Fatalf("removed = %v", removed)
	}
	var doc catalogDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	e := findNVIDIA(doc, gpuOperatorSlug)
	if e == nil {
		t.Fatal("gpu-operator missing")
	}
	if e.Name != "GPU Operator (renamed)" {
		t.Fatalf("Name not refreshed: %q", e.Name)
	}
	if e.Description != "fresh description" {
		t.Fatalf("Description not refreshed: %q", e.Description)
	}
	if len(e.Labels) != 1 || e.Labels[0].Code != supportedCode {
		t.Fatalf("Supported label missing after refresh: %+v", e.Labels)
	}
}

func TestSyncNVAIE_OverridePinsFields(t *testing.T) {
	// An override pins Description; the NGC-derived value is ignored for that field
	// while every other field still refreshes from derived.
	derived := []catalog.Item{{
		Name: "GPU Operator (renamed)", SlugName: gpuOperatorSlug,
		Description: "ngc description", PackagingFormat: helmChart, RepositoryURL: nvidiaHelmRepo,
	}}
	ov := mustOverrides(t, `{"gpu-operator":{"description":"pinned description"}}`)
	out, _, _, err := syncNVAIE([]byte(baseCatalog), derived, ov)
	if err != nil {
		t.Fatal(err)
	}
	var doc catalogDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	e := findNVIDIA(doc, gpuOperatorSlug)
	if e == nil {
		t.Fatal("gpu-operator missing")
	}
	if e.Description != "pinned description" {
		t.Fatalf("Description not pinned: %q", e.Description)
	}
	if e.Name != "GPU Operator (renamed)" {
		t.Fatalf("non-pinned Name should refresh: %q", e.Name)
	}
}

func TestSyncNVAIE_RemovesStaleOwnedEntry(t *testing.T) {
	// gpu-operator is owned but absent from derived: it must be removed and reported.
	out, added, removed, err := syncNVAIE([]byte(baseCatalog), nil, overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Fatalf("added = %v", added)
	}
	if len(removed) != 1 || removed[0] != gpuOperatorSlug {
		t.Fatalf("removed = %v, want [gpu-operator]", removed)
	}
	var doc catalogDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if findNVIDIA(doc, gpuOperatorSlug) != nil {
		t.Fatal("stale owned entry not removed")
	}
}

func TestSyncNVAIE_PreservesUnownedEntries(t *testing.T) {
	// deepstream-its carries no Supported chip: it is hand-added and must survive a
	// run that removes the owned gpu-operator entry, untouched.
	out, _, _, err := syncNVAIE([]byte(baseCatalog), nil, overrides{})
	if err != nil {
		t.Fatal(err)
	}
	var doc catalogDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	e := findNVIDIA(doc, deepstreamITSSlug)
	if e == nil {
		t.Fatal("unowned entry removed")
	}
	if e.Name != "DeepStream ITS" || len(e.Labels) != 0 {
		t.Fatalf("unowned entry modified: %+v", *e)
	}
}

func TestSyncNVAIE_PromotesUnownedWhenNowSupported(t *testing.T) {
	// NGC now reports deepstream-its as supported: the derived entry takes over the
	// slug, replacing the unowned entry and gaining the Supported chip. The slug
	// already existed, so it is not reported as added.
	derived := []catalog.Item{{
		Name: "DeepStream ITS", SlugName: deepstreamITSSlug,
		Description: "now supported", PackagingFormat: helmChart, RepositoryURL: nvidiaHelmRepo,
	}}
	out, added, _, err := syncNVAIE([]byte(baseCatalog), derived, overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Fatalf("promoted pre-existing slug must not be added: %v", added)
	}
	var doc catalogDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	// Exactly one deepstream-its entry, now owned.
	n := 0
	for _, e := range doc.NVIDIA {
		if e.SlugName == deepstreamITSSlug {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want 1 deepstream-its entry, got %d", n)
	}
	e := findNVIDIA(doc, deepstreamITSSlug)
	if len(e.Labels) != 1 || e.Labels[0].Code != supportedCode {
		t.Fatalf("promoted entry not owned: %+v", e.Labels)
	}
	if e.Description != "now supported" {
		t.Fatalf("promoted entry not from derived: %q", e.Description)
	}
}

func TestSyncNVAIE_PreservesSuseAI(t *testing.T) {
	out, _, _, err := syncNVAIE([]byte(baseCatalog), nil, overrides{})
	if err != nil {
		t.Fatal(err)
	}
	var doc catalogDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.SuseAI) != 1 || doc.SuseAI[0].SlugName != "milvus" {
		t.Fatalf("suse-ai changed: %+v", doc.SuseAI)
	}
	if strings.Index(string(out), `"suse-ai"`) > strings.Index(string(out), `"nvidia"`) {
		t.Fatal("top-level key order must stay suse-ai then nvidia")
	}
}

func TestSyncNVAIE_DeduplicatesSlugPreferNim(t *testing.T) {
	// The same chart is published under two NGC repos; they collide on slug_name,
	// which the UI keys tiles/routing on. Dedupe keeps exactly one, preferring
	// nim/nvidia.
	const slug = "nvidia-nim-llama-32-nv-embedqa-1b-v2"
	derived := []catalog.Item{
		{
			Name: "NVIDIA NIM for Text Embedding", SlugName: slug,
			PackagingFormat: helmChart,
			RepositoryURL:   nemoHelmRepo,
		},
		{
			Name: "NVIDIA NIM for Text Embedding", SlugName: slug,
			PackagingFormat: helmChart, RepositoryURL: nimHelmRepo,
		},
	}
	out, _, _, err := syncNVAIE([]byte(baseCatalog), derived, overrides{})
	if err != nil {
		t.Fatal(err)
	}
	var doc catalogDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	var matches []catalog.Item
	for _, e := range doc.NVIDIA {
		if e.SlugName == slug {
			matches = append(matches, e)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("want exactly 1 entry for slug %q, got %d", slug, len(matches))
	}
	if matches[0].RepositoryURL != nimHelmRepo {
		t.Fatalf("dedupe kept the wrong repo: %q (want nim/nvidia)", matches[0].RepositoryURL)
	}
}

func TestSyncNVAIE_OverrideSurvivesDedupe(t *testing.T) {
	// The override is applied to every derived copy of a slug before dedupe, so it
	// is present on whichever copy dedupe keeps.
	const slug = "embed"
	derived := []catalog.Item{
		{Name: "Embed", SlugName: slug, PackagingFormat: helmChart, RepositoryURL: nemoHelmRepo},
		{Name: "Embed", SlugName: slug, PackagingFormat: helmChart, RepositoryURL: nimHelmRepo},
	}
	ov := mustOverrides(t, `{"embed":{"description":"pinned"}}`)
	out, _, _, err := syncNVAIE([]byte(baseCatalog), derived, ov)
	if err != nil {
		t.Fatal(err)
	}
	var doc catalogDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	e := findNVIDIA(doc, slug)
	if e == nil {
		t.Fatal("embed missing")
	}
	if e.RepositoryURL != nimHelmRepo {
		t.Fatalf("dedupe kept wrong repo: %q", e.RepositoryURL)
	}
	if e.Description != "pinned" {
		t.Fatalf("override lost through dedupe: %q", e.Description)
	}
}

func TestSyncNVAIE_OverrideDoesNotBlockRemoval(t *testing.T) {
	// An override for a slug NGC no longer lists does not resurrect it: removal is
	// driven purely by NGC presence.
	ov := mustOverrides(t, `{"gpu-operator":{"description":"pinned"}}`)
	out, _, removed, err := syncNVAIE([]byte(baseCatalog), nil, ov)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != gpuOperatorSlug {
		t.Fatalf("removed = %v, want [gpu-operator]", removed)
	}
	var doc catalogDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if findNVIDIA(doc, gpuOperatorSlug) != nil {
		t.Fatal("override resurrected a delisted chart")
	}
}

func TestSyncNVAIE_Idempotent(t *testing.T) {
	derived := []catalog.Item{
		{Name: "GPU Operator", SlugName: gpuOperatorSlug, PackagingFormat: helmChart, RepositoryURL: nvidiaHelmRepo},
		{
			Name: "Multimodal Safety NIM", SlugName: multimodalSafetyNIMSlug,
			PackagingFormat: helmChart, RepositoryURL: nimHelmRepo,
		},
	}
	ov := mustOverrides(t, `{"gpu-operator":{"description":"pinned"}}`)
	out1, _, _, err := syncNVAIE([]byte(baseCatalog), derived, ov)
	if err != nil {
		t.Fatal(err)
	}
	out2, added, removed, err := syncNVAIE(out1, derived, ov)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("second run must be a no-op: added %v removed %v", added, removed)
	}
	if string(out1) != string(out2) {
		t.Fatal("second run produced a different document (not idempotent)")
	}
}
