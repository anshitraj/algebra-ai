package routing

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func ans(provider, body string) Answer { return Answer{Provider: provider, Body: []byte(body)} }

func TestCrossCheck_PricesWithinTheToleranceAgree(t *testing.T) {
	got := CompareAnswers("token.price", []string{"mint"}, []Answer{
		ans("alpha", `{"provider":"alpha","mint":"So111","price_usd":186.20,"as_of":"2026-10-30T10:00:00Z","note":"x"}`),
		ans("beta", `{"provider":"beta","mint":"So111","price_usd":186.60,"as_of":"2026-10-30T10:00:03Z","note":"y"}`),
	})
	// What names the seller, the moment or the question is not what is being checked.
	if got.Verdict != CheckAgree || got.Compared != 1 || got.Agreed != 1 || len(got.Differences) != 0 {
		t.Fatalf("0.2%% apart on a 1%% tolerance is agreement, on the one field that counts: %+v", got)
	}
	if !strings.Contains(got.Summary, "agree") || !strings.Contains(got.Summary, "1%") {
		t.Errorf("summary: %q", got.Summary)
	}
}

func TestCrossCheck_PricesApartDifferAndSayHowFar(t *testing.T) {
	got := CompareAnswers("token.price", nil, []Answer{
		ans("alpha", `{"price_usd":186.20}`),
		ans("beta", `{"price_usd":189.90}`),
	})
	if got.Verdict != CheckDiffer || len(got.Differences) != 1 {
		t.Fatalf("%+v", got)
	}
	d := got.Differences[0]
	if d.Path != "price_usd" || d.Values["alpha"] != "186.2" || d.Values["beta"] != "189.9" || d.SpreadPct == nil || *d.SpreadPct != 1.95 {
		t.Errorf("the difference names the field, who said what and how far apart: %+v", d)
	}
	if d.Outlier != "" {
		t.Errorf("with two answers nobody is the odd one out: %q", d.Outlier)
	}
	if !strings.Contains(got.Summary, "price_usd (186.2 vs 189.9, 1.95% apart)") {
		t.Errorf("summary: %q", got.Summary)
	}
}

func TestCrossCheck_TheOddOneOut(t *testing.T) {
	got := CompareAnswers("token.price", nil, []Answer{
		ans("alpha", `{"price_usd":186.2}`),
		ans("beta", `{"price_usd":186.4}`),
		ans("gamma", `{"price_usd":195}`),
	})
	if got.Verdict != CheckDiffer || len(got.Outliers) != 1 || got.Outliers[0] != "gamma" || got.Differences[0].Outlier != "gamma" {
		t.Fatalf("two agree and one is alone: %+v", got)
	}
	if !strings.Contains(got.Summary, "gamma is the odd one out") {
		t.Errorf("summary: %q", got.Summary)
	}

	// All three apart: nobody is singled out.
	spread := CompareAnswers("token.price", nil, []Answer{
		ans("alpha", `{"price_usd":100}`), ans("beta", `{"price_usd":110}`), ans("gamma", `{"price_usd":125}`),
	})
	if spread.Verdict != CheckDiffer || len(spread.Outliers) != 0 {
		t.Errorf("no clear odd one out: %+v", spread)
	}

	// The same for words: a strict majority says one thing.
	words := CompareAnswers("x", nil, []Answer{
		ans("alpha", `{"risk":"low"}`), ans("beta", `{"risk":"Low"}`), ans("gamma", `{"risk":"high"}`),
	})
	if words.Verdict != CheckDiffer || len(words.Outliers) != 1 || words.Outliers[0] != "gamma" {
		t.Errorf("text agrees whatever its case, and the dissenter is named: %+v", words)
	}
}

func TestCrossCheck_TextAndBooleans(t *testing.T) {
	got := CompareAnswers("x", nil, []Answer{
		ans("alpha", `{"status":"ok","open":true,"closed_at":null}`),
		ans("beta", `{"status":"OK","open":false,"closed_at":null}`),
	})
	if got.Verdict != CheckDiffer || got.Compared != 3 || got.Agreed != 2 || len(got.Differences) != 1 || got.Differences[0].Path != "open" {
		t.Fatalf("%+v", got)
	}
}

func TestCrossCheck_ANumberSentAsTextIsStillANumber(t *testing.T) {
	got := CompareAnswers("token.price", nil, []Answer{
		ans("alpha", `{"price":"1,204.5"}`),
		ans("beta", `{"price":1204.9}`),
	})
	if got.Verdict != CheckAgree {
		t.Fatalf("%+v", got)
	}
}

func TestCrossCheck_RecordsAreMatchedByWhatIdentifiesThem(t *testing.T) {
	got := CompareAnswers("web.search", []string{"query"}, []Answer{
		ans("alpha", `{"query":"q","results":[{"url":"https://a.example","rank":1,"snippet":"x"},{"url":"https://b.example","rank":2,"snippet":"y"}]}`),
		ans("beta", `{"query":"q","results":[{"url":"https://b.example","rank":2,"snippet":"y"},{"url":"https://c.example","rank":1,"snippet":"z"}]}`),
	})
	// Only the one record both gave is compared; the ones only one gave are not "differences".
	if got.Verdict != CheckAgree || got.Compared != 2 || got.Agreed != 2 {
		t.Fatalf("%+v", got)
	}
	disagree := CompareAnswers("web.search", nil, []Answer{
		ans("alpha", `{"results":[{"url":"https://b.example","rank":2}]}`),
		ans("beta", `{"results":[{"url":"https://b.example","rank":5}]}`),
	})
	if disagree.Verdict != CheckDiffer || disagree.Differences[0].Path != "results[https://b.example].rank" {
		t.Errorf("the record is named by its address: %+v", disagree)
	}
}

func TestCrossCheck_LongProseIsNotADifference(t *testing.T) {
	a, b := strings.Repeat("alpha says this. ", 20), strings.Repeat("beta says that. ", 20)
	got := CompareAnswers("web.scrape", nil, []Answer{
		ans("alpha", fmt.Sprintf(`{"markdown":%q,"words":10}`, a)),
		ans("beta", fmt.Sprintf(`{"markdown":%q,"words":10}`, b)),
	})
	if got.Verdict != CheckAgree || got.Compared != 1 {
		t.Fatalf("two different pages of prose don't disagree, the numbers do or don't: %+v", got)
	}
	only := CompareAnswers("web.scrape", nil, []Answer{
		ans("alpha", fmt.Sprintf(`{"markdown":%q}`, a)),
		ans("beta", fmt.Sprintf(`{"markdown":%q}`, b)),
	})
	if only.Verdict != CheckUnclear || only.Compared != 0 {
		t.Errorf("nothing comparable is unclear, never agreement: %+v", only)
	}
	same := CompareAnswers("web.scrape", nil, []Answer{
		ans("alpha", fmt.Sprintf(`{"markdown":%q}`, a)), ans("beta", fmt.Sprintf(`{"markdown":%q}`, a)),
	})
	if same.Verdict != CheckAgree {
		t.Errorf("the same page twice is agreement: %+v", same)
	}
}

func TestCrossCheck_AnAnswerThatIsNotJSONIsLeftOut(t *testing.T) {
	got := CompareAnswers("x", nil, []Answer{ans("alpha", `{"a":1}`), ans("beta", `plain text, not JSON`)})
	if got.Verdict != CheckUnclear || !reflect.DeepEqual(got.Unreadable, []string{"beta"}) || !reflect.DeepEqual(got.Providers, []string{"alpha"}) {
		t.Fatalf("%+v", got)
	}
	three := CompareAnswers("x", nil, []Answer{ans("alpha", `{"a":1}`), ans("beta", `nope`), ans("gamma", `{"a":1}`)})
	if three.Verdict != CheckAgree || !reflect.DeepEqual(three.Providers, []string{"alpha", "gamma"}) {
		t.Errorf("the two that can be read are compared: %+v", three)
	}
}

func TestCrossCheck_ListsOfWordsAreSets(t *testing.T) {
	got := CompareAnswers("x", nil, []Answer{ans("alpha", `{"tags":["a","b"]}`), ans("beta", `{"tags":["b","a"]}`)})
	if got.Verdict != CheckAgree {
		t.Fatalf("the order of a list of words is not an answer: %+v", got)
	}
	differ := CompareAnswers("x", nil, []Answer{ans("alpha", `{"tags":["a","b"]}`), ans("beta", `{"tags":["a","c"]}`)})
	if differ.Verdict != CheckDiffer {
		t.Errorf("%+v", differ)
	}
}

func TestCrossCheck_ToleranceIsPerKindOfWork(t *testing.T) {
	if CheckToleranceFor("token.price") != 0.01 || CheckToleranceFor("wallet.balances") != 0.001 || CheckToleranceFor("anything.else") != 0.02 {
		t.Fatal("tolerances")
	}
	body := []Answer{ans("alpha", `{"usdc":100}`), ans("beta", `{"usdc":100.2}`)}
	if got := CompareAnswers("wallet.balances", nil, body); got.Verdict != CheckDiffer {
		t.Errorf("a balance 0.2%% apart is two different balances: %+v", got)
	}
	if got := CompareAnswers("anything.else", nil, body); got.Verdict != CheckAgree {
		t.Errorf("the same gap is nothing for other work: %+v", got)
	}
}

func TestCrossCheck_ManyDifferencesAreBoundedAndTheWidestComeFirst(t *testing.T) {
	var a, b strings.Builder
	a.WriteString("{")
	b.WriteString("{")
	for i := 0; i < 25; i++ {
		sep := ","
		if i == 24 {
			sep = ""
		}
		fmt.Fprintf(&a, `"f%02d":100%s`, i, sep)
		fmt.Fprintf(&b, `"f%02d":%d%s`, i, 100+10+i, sep) // 10% to 34% apart, the widest last
	}
	a.WriteString("}")
	b.WriteString("}")
	got := CompareAnswers("x", nil, []Answer{ans("alpha", a.String()), ans("beta", b.String())})
	if got.Verdict != CheckDiffer || len(got.Differences) != 20 || got.MoreDifferences != 5 || got.Compared != 25 {
		t.Fatalf("%d differences listed, %d more, %d compared", len(got.Differences), got.MoreDifferences, got.Compared)
	}
	if got.Differences[0].Path != "f24" || *got.Differences[0].SpreadPct < *got.Differences[19].SpreadPct {
		t.Errorf("the widest first: %s %v ... %s %v", got.Differences[0].Path, *got.Differences[0].SpreadPct, got.Differences[19].Path, *got.Differences[19].SpreadPct)
	}
	if !strings.Contains(got.Summary, "25 of the 25") && !strings.Contains(got.Summary, "differ on 25") {
		t.Errorf("summary counts all of them: %q", got.Summary)
	}
}

func TestCrossCheck_TheSameAnswersReadTheSameWayTwice(t *testing.T) {
	in := []Answer{
		ans("alpha", `{"a":1,"b":"x","c":[{"id":"1","v":2},{"id":"2","v":3}]}`),
		ans("beta", `{"a":2,"b":"y","c":[{"id":"2","v":4},{"id":"1","v":2}]}`),
		ans("gamma", `{"a":1,"b":"x","c":[{"id":"1","v":2}]}`),
	}
	first, second := CompareAnswers("x", nil, in), CompareAnswers("x", nil, in)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("not deterministic:\n%+v\n%+v", first, second)
	}
}

func TestCrossCheck_NothingTooBigOrTooDeepIsRead(t *testing.T) {
	deep := strings.Repeat(`{"a":`, 30) + "1" + strings.Repeat("}", 30)
	got := CompareAnswers("x", nil, []Answer{ans("alpha", deep), ans("beta", deep)})
	if got.Verdict != CheckUnclear {
		t.Errorf("a leaf buried 30 levels down is not read: %+v", got)
	}
	big := Answer{Provider: "alpha", Body: []byte(`{"a":"` + strings.Repeat("x", maxCheckBody) + `"}`)}
	if got := CompareAnswers("x", nil, []Answer{big, ans("beta", `{"a":1}`)}); got.Verdict != CheckUnclear || len(got.Unreadable) != 1 {
		t.Errorf("an answer over the size limit is left out: %+v", got)
	}
}
