package plugin

import "testing"

func TestResolve(t *testing.T) {
	got := Resolve(map[string]bool{WebPrices: false, AmazonDeals: false, RedditDeals: true, "not_a_plugin": true})
	if !got[WebPrices] {
		t.Error("a core plugin stays on even if saved off")
	}
	if got[AmazonDeals] {
		t.Error("a saved choice overrides a default-on plugin")
	}
	if !got[RedditDeals] || got[DesiDimeDeals] {
		t.Error("community plugins are off unless chosen")
	}
	if !got[BankOffers] {
		t.Error("an unsaved default-on plugin is on")
	}
	if _, ok := got["not_a_plugin"]; ok {
		t.Error("unknown IDs never appear")
	}
}

func TestCommunityPluginsNeverCheckout(t *testing.T) {
	for _, p := range Catalog {
		if p.Purpose == PurposeCommunity && (p.DefaultOn || p.Trust != TrustCommunity || len(SitesFor(p, Config{})) == 0) {
			t.Errorf("%s: community plugins must be opt-in, labelled community, and name their sites", p.ID)
		}
	}
}

func TestSubreddits(t *testing.T) {
	got, err := NormalizeSubreddits([]string{" r/Fitness_India ", "/r/dealsforindia/", "fitness_india"})
	if err != nil || len(got) != 2 || got[0] != "Fitness_India" || got[1] != "dealsforindia" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := NormalizeSubreddits([]string{"a", "b c"}); err == nil {
		t.Error("an impossible subreddit name is refused")
	}
	if _, err := NormalizeSubreddits([]string{"one", "two", "three"}); err == nil {
		t.Error("more than two subreddits is refused")
	}
	p, _ := Get(RedditDeals)
	sites := SitesFor(p, Config{Subreddits: []string{"IndianFitness"}})
	if len(sites) != 1 || sites[0].Path != "/r/IndianFitness" || sites[0].Host != "reddit.com" {
		t.Errorf("custom subreddit sites wrong: %+v", sites)
	}
	if len(SitesFor(p, Config{})) != 2 {
		t.Error("no choice means the default two subreddits")
	}
}
