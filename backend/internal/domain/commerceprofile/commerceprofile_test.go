package commerceprofile

import (
	"reflect"
	"testing"
)

func TestMergePreferences_AddsNewCategory(t *testing.T) {
	p := Empty("user_1")
	p.MergePreferences("clothing", map[string]any{"usual_size": "L"})

	if got := p.Preferences["clothing"]["usual_size"]; got != "L" {
		t.Errorf("usual_size = %v, want L", got)
	}
}

func TestMergePreferences_MergesWithoutClobberingOtherKeys(t *testing.T) {
	p := Empty("user_1")
	p.MergePreferences("clothing", map[string]any{"usual_size": "L", "preferred_fit": "regular"})
	p.MergePreferences("clothing", map[string]any{"preferred_colors": []string{"black", "navy"}})

	want := map[string]any{"usual_size": "L", "preferred_fit": "regular", "preferred_colors": []string{"black", "navy"}}
	if !reflect.DeepEqual(p.Preferences["clothing"], want) {
		t.Errorf("clothing = %#v, want %#v", p.Preferences["clothing"], want)
	}
}

func TestMergePreferences_OverwritesSameKey(t *testing.T) {
	p := Empty("user_1")
	p.MergePreferences("clothing", map[string]any{"usual_size": "M"})
	p.MergePreferences("clothing", map[string]any{"usual_size": "L"})

	if got := p.Preferences["clothing"]["usual_size"]; got != "L" {
		t.Errorf("usual_size = %v, want L (later write should win for the same key)", got)
	}
}

func TestMergePreferences_DoesNotTouchOtherCategories(t *testing.T) {
	p := Empty("user_1")
	p.MergePreferences("clothing", map[string]any{"usual_size": "L"})
	p.MergePreferences("food", map[string]any{"dietary": "vegetarian"})

	if _, ok := p.Preferences["clothing"]["dietary"]; ok {
		t.Error("food preference leaked into clothing category")
	}
	if got := p.Preferences["clothing"]["usual_size"]; got != "L" {
		t.Errorf("clothing.usual_size was clobbered by a later merge into a different category: got %v", got)
	}
}

func TestEmpty_HasNoNilMaps(t *testing.T) {
	p := Empty("user_1")
	if p.Preferences == nil {
		t.Fatal("Empty() must return a non-nil Preferences map so callers can range over it safely")
	}
	if len(p.Preferences) != 0 {
		t.Errorf("expected an empty profile to have zero preference categories, got %d", len(p.Preferences))
	}
}
