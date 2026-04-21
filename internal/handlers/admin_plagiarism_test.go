package handlers

import (
	"testing"

	"github.com/mfonda/simhash"
)

// Verify that two cosmetically-different but semantically-identical C++
// sources land within the default 85% similarity threshold, and that clearly
// different sources land outside it.
func TestPlagCheck_SimhashDetectsCosmeticRewrites(t *testing.T) {
	const src1 = `
#include <bits/stdc++.h>
using namespace std;

// read n numbers, print sum
int main() {
    int n;
    cin >> n;
    long long sum = 0;
    for (int i = 0; i < n; i++) {
        int x; cin >> x;
        sum += x;
    }
    cout << sum << endl;
    return 0;
}
`
	// Same logic, renamed variables, added blank lines + different comments.
	const src2 = `
#include <iostream>
using namespace std;

/* totally different comment block */
int main() {

    int count;
    cin >> count;

    long long total = 0;

    for (int idx = 0; idx < count; idx++) {
        int v;
        cin >> v;
        total += v;
    }

    cout << total << endl;
    return 0;
}
`
	// Structurally different program (string reversal).
	const src3 = `
#include <bits/stdc++.h>
using namespace std;
int main() {
    string s;
    cin >> s;
    reverse(s.begin(), s.end());
    cout << s << endl;
    return 0;
}
`

	f1 := cppFeatures(src1)
	f2 := cppFeatures(src2)
	f3 := cppFeatures(src3)

	if len(f1) < 5 || len(f2) < 5 || len(f3) < 5 {
		t.Fatalf("expected >=5 features per source, got %d/%d/%d", len(f1), len(f2), len(f3))
	}

	h1 := simhash.SimhashBytes(f1)
	h2 := simhash.SimhashBytes(f2)
	h3 := simhash.SimhashBytes(f3)

	d12 := int(simhash.Compare(h1, h2))
	d13 := int(simhash.Compare(h1, h3))
	d23 := int(simhash.Compare(h2, h3))
	sim12 := 1.0 - float64(d12)/64.0
	sim13 := 1.0 - float64(d13)/64.0
	sim23 := 1.0 - float64(d23)/64.0

	t.Logf("sim(sum-rename)=%d/64 (%.1f%%), sim(sum-reverse)=%d/64 (%.1f%%), sim(rename-reverse)=%d/64 (%.1f%%)",
		d12, sim12*100, d13, sim13*100, d23, sim23*100)

	// Cosmetic rewrite should match at the default 85% threshold (distance <= 10).
	if d12 > 10 {
		t.Errorf("expected rename-only rewrite to be near-duplicate (dist <= 10), got %d", d12)
	}
	// Different programs should be clearly below the threshold.
	if d13 <= 10 {
		t.Errorf("expected unrelated programs to differ (dist > 10), got %d", d13)
	}
	if d23 <= 10 {
		t.Errorf("expected unrelated programs to differ (dist > 10), got %d", d23)
	}
}

func TestPlagCheck_NormalizeCppStripsNoise(t *testing.T) {
	src := `// header comment
#include <bits/stdc++.h>
using namespace std;
/* block
   comment */
int main() {
    string s = "hello world";
    char c = 'x';
    cout << s << c << endl;
    return 0;
}
`
	n := normalizeCpp(src)
	mustNotContain := []string{
		"hello world",           // string literal body
		"//",                    // line-comment marker
		"/*",                    // block-comment marker
		"#include",              // preprocessor
		"using namespace",       // namespace using
	}
	for _, needle := range mustNotContain {
		if containsFold(n, needle) {
			t.Errorf("normalized output still contains %q: %q", needle, n)
		}
	}
}

func TestPlagCheck_UnionFindBuildsClusters(t *testing.T) {
	uf := newUnionFind()
	// Connect 1-2, 2-3, and 4-5 — expect two components: {1,2,3} and {4,5}.
	uf.union("u1", "u2")
	uf.union("u2", "u3")
	uf.union("u4", "u5")

	if uf.find("u1") != uf.find("u3") {
		t.Errorf("expected u1 and u3 in same component")
	}
	if uf.find("u1") == uf.find("u4") {
		t.Errorf("expected u1 and u4 in different components")
	}
}

func containsFold(haystack, needle string) bool {
	// naive case-sensitive contains; normalizeCpp lowercases, so lowercase
	// comparison is fine for the strings we check above.
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
