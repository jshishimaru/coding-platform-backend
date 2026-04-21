// Manual, contest-scoped plagiarism check. Compares the latest submission
// per (user, problem) in one contest using SimHash over normalized C++ token
// k-grams, then emits similar pairs and union-find clusters above a
// configurable similarity threshold (default 85%).
//
// The check is synchronous, ephemeral (no DB storage), and access-gated via
// canManageContest so group admins can only run it on contests in groups
// they manage.

package handlers

import (
	"context"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mfonda/simhash"
)

// ──────────────────────────────────────────────────────────────
// Request / response types
// ──────────────────────────────────────────────────────────────

type PlagCheckRequest struct {
	Threshold float64 `json:"threshold"` // 0..1, default 0.85
}

type PlagCheckPair struct {
	ProblemID   int     `json:"problem_id"`
	ProblemSlug string  `json:"problem_slug"`
	UserAID     int     `json:"user_a_id"`
	UserAName   string  `json:"user_a_username"`
	SubAID      int     `json:"submission_a_id"`
	UserBID     int     `json:"user_b_id"`
	UserBName   string  `json:"user_b_username"`
	SubBID      int     `json:"submission_b_id"`
	Similarity  float64 `json:"similarity"`
	HammingDist int     `json:"hamming_distance"`
}

type PlagCheckCluster struct {
	ProblemID   int      `json:"problem_id"`
	ProblemSlug string   `json:"problem_slug"`
	UserIDs     []int    `json:"user_ids"`
	Usernames   []string `json:"usernames"`
}

type PlagCheckResponse struct {
	Threshold        float64            `json:"threshold"`
	ComparedCount    int                `json:"compared_count"`
	EligibleCount    int                `json:"eligible_count"`
	MatchedUserCount int                `json:"matched_user_count"`
	Matches          []PlagCheckPair    `json:"matches"`
	Clusters         []PlagCheckCluster `json:"clusters"`
	DurationMs       int64              `json:"duration_ms"`
}

// ──────────────────────────────────────────────────────────────
// Handler
// ──────────────────────────────────────────────────────────────

func (h *Handler) AdminContestPlagCheck(c *gin.Context) {
	start := time.Now()

	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid, _ := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	ctx := context.Background()

	ok, err := h.canManageContest(ctx, uid, roleStr, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate contest permissions"})
		return
	}
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not manage this contest"})
		return
	}

	var req PlagCheckRequest
	_ = c.ShouldBindJSON(&req)
	threshold := req.Threshold
	if threshold <= 0 || threshold > 1 {
		threshold = 0.85
	}
	// Hamming-distance budget: 64 * (1 - threshold), rounded to nearest int.
	maxDist := int(math.Round(64 * (1.0 - threshold)))
	if maxDist < 0 {
		maxDist = 0
	}

	rows, err := h.DB.Query(ctx,
		`SELECT DISTINCT ON (s.user_id, s.problem_id)
		        s.id, s.user_id, u.username, s.problem_id, p.slug, s.source_code
		 FROM app.submissions s
		 JOIN app.users u    ON u.id = s.user_id
		 JOIN app.problems p ON p.id = s.problem_id
		 WHERE s.contest_id = $1
		 ORDER BY s.user_id, s.problem_id, s.submitted_at DESC`,
		contestID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	type entry struct {
		subID       int
		userID      int
		username    string
		problemID   int
		problemSlug string
		hash        uint64
		tokenCount  int
	}

	// Bucket entries by problem; pairwise comparison is per-problem.
	buckets := map[int][]entry{}
	slugByProblem := map[int]string{}
	compared := 0
	eligible := 0

	for rows.Next() {
		var e entry
		var src string
		if err := rows.Scan(&e.subID, &e.userID, &e.username, &e.problemID, &e.problemSlug, &src); err != nil {
			continue
		}
		compared++

		features := cppFeatures(src)
		if len(features) < 5 {
			// Too short to fingerprint meaningfully; count but skip.
			continue
		}
		e.tokenCount = len(features)
		e.hash = simhash.SimhashBytes(features)
		slugByProblem[e.problemID] = e.problemSlug
		buckets[e.problemID] = append(buckets[e.problemID], e)
		eligible++
	}

	// Pairwise compare per problem.
	matches := make([]PlagCheckPair, 0)
	// Union-find keyed by "problem:userID" so clusters are per-problem.
	uf := newUnionFind()
	userMeta := map[string]struct {
		userID      int
		username    string
		problemID   int
		problemSlug string
	}{}

	ufKey := func(problemID, userID int) string {
		return strconv.Itoa(problemID) + ":" + strconv.Itoa(userID)
	}

	for pid, bucket := range buckets {
		for i := 0; i < len(bucket); i++ {
			for j := i + 1; j < len(bucket); j++ {
				a, b := bucket[i], bucket[j]
				dist := int(simhash.Compare(a.hash, b.hash))
				if dist > maxDist {
					continue
				}
				sim := 1.0 - float64(dist)/64.0

				// Ensure a stable (userA, userB) ordering by user ID.
				ua, ub := a, b
				if ua.userID > ub.userID {
					ua, ub = ub, ua
				}
				matches = append(matches, PlagCheckPair{
					ProblemID:   pid,
					ProblemSlug: slugByProblem[pid],
					UserAID:     ua.userID,
					UserAName:   ua.username,
					SubAID:      ua.subID,
					UserBID:     ub.userID,
					UserBName:   ub.username,
					SubBID:      ub.subID,
					Similarity:  sim,
					HammingDist: dist,
				})

				ka, kb := ufKey(pid, ua.userID), ufKey(pid, ub.userID)
				uf.union(ka, kb)
				userMeta[ka] = struct {
					userID      int
					username    string
					problemID   int
					problemSlug string
				}{ua.userID, ua.username, pid, slugByProblem[pid]}
				userMeta[kb] = struct {
					userID      int
					username    string
					problemID   int
					problemSlug string
				}{ub.userID, ub.username, pid, slugByProblem[pid]}
			}
		}
	}

	// Build clusters from union-find components.
	type clusterAcc struct {
		problemID   int
		problemSlug string
		users       map[int]string
	}
	clusterMap := map[string]*clusterAcc{}
	matchedUsers := map[int]struct{}{}

	for key, meta := range userMeta {
		root := uf.find(key)
		acc, exists := clusterMap[root]
		if !exists {
			acc = &clusterAcc{
				problemID:   meta.problemID,
				problemSlug: meta.problemSlug,
				users:       map[int]string{},
			}
			clusterMap[root] = acc
		}
		acc.users[meta.userID] = meta.username
		matchedUsers[meta.userID] = struct{}{}
	}

	clusters := make([]PlagCheckCluster, 0, len(clusterMap))
	for _, acc := range clusterMap {
		if len(acc.users) < 2 {
			continue
		}
		userIDs := make([]int, 0, len(acc.users))
		for id := range acc.users {
			userIDs = append(userIDs, id)
		}
		sort.Ints(userIDs)
		usernames := make([]string, 0, len(userIDs))
		for _, id := range userIDs {
			usernames = append(usernames, acc.users[id])
		}
		clusters = append(clusters, PlagCheckCluster{
			ProblemID:   acc.problemID,
			ProblemSlug: acc.problemSlug,
			UserIDs:     userIDs,
			Usernames:   usernames,
		})
	}

	// Stable output order: clusters by problem then first user, matches by
	// similarity desc (then problem, then user A).
	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].ProblemID != clusters[j].ProblemID {
			return clusters[i].ProblemID < clusters[j].ProblemID
		}
		return clusters[i].UserIDs[0] < clusters[j].UserIDs[0]
	})
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Similarity != matches[j].Similarity {
			return matches[i].Similarity > matches[j].Similarity
		}
		if matches[i].ProblemID != matches[j].ProblemID {
			return matches[i].ProblemID < matches[j].ProblemID
		}
		return matches[i].UserAID < matches[j].UserAID
	})

	resp := PlagCheckResponse{
		Threshold:        threshold,
		ComparedCount:    compared,
		EligibleCount:    eligible,
		MatchedUserCount: len(matchedUsers),
		Matches:          matches,
		Clusters:         clusters,
		DurationMs:       time.Since(start).Milliseconds(),
	}

	h.logAudit(uid, "contest.plag_check", "contest", contestID, map[string]interface{}{
		"threshold":     threshold,
		"compared":      compared,
		"eligible":      eligible,
		"matches":       len(matches),
		"matched_users": len(matchedUsers),
	}, c.ClientIP())

	c.JSON(http.StatusOK, resp)
}

// ──────────────────────────────────────────────────────────────
// C++ normalization + feature extraction
// ──────────────────────────────────────────────────────────────

// Regexes compiled once at package init.
var (
	reLineComment  = regexp.MustCompile(`(?m)//[^\n]*`)
	reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reStringLit    = regexp.MustCompile("\"(?:\\\\.|[^\"\\\\])*\"")
	reCharLit      = regexp.MustCompile(`'(?:\\.|[^'\\])*'`)
	rePreproc      = regexp.MustCompile(`(?m)^\s*#.*$`)
	reUsingNS      = regexp.MustCompile(`(?m)^\s*using\s+namespace[^;]*;`)
	reIdent        = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	reNumber       = regexp.MustCompile(`^[0-9]+$`)
	reToken        = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*|[0-9]+|[^\sA-Za-z0-9_]`)
)

// cppKeywords is the set of lexical tokens we treat as structurally
// meaningful. Everything else that matches `reIdent` is considered a
// user-chosen identifier and canonicalized to "id" so rename-only rewrites
// collapse onto the same fingerprint.
var cppKeywords = map[string]struct{}{
	// Core C/C++ keywords
	"alignas": {}, "alignof": {}, "and": {}, "and_eq": {}, "asm": {},
	"auto": {}, "bitand": {}, "bitor": {}, "bool": {}, "break": {},
	"case": {}, "catch": {}, "char": {}, "char16_t": {}, "char32_t": {},
	"class": {}, "compl": {}, "const": {}, "constexpr": {}, "const_cast": {},
	"continue": {}, "decltype": {}, "default": {}, "delete": {}, "do": {},
	"double": {}, "dynamic_cast": {}, "else": {}, "enum": {}, "explicit": {},
	"export": {}, "extern": {}, "false": {}, "float": {}, "for": {},
	"friend": {}, "goto": {}, "if": {}, "inline": {}, "int": {},
	"long": {}, "mutable": {}, "namespace": {}, "new": {}, "noexcept": {},
	"not": {}, "not_eq": {}, "nullptr": {}, "operator": {}, "or": {},
	"or_eq": {}, "private": {}, "protected": {}, "public": {}, "register": {},
	"reinterpret_cast": {}, "return": {}, "short": {}, "signed": {}, "sizeof": {},
	"static": {}, "static_assert": {}, "static_cast": {}, "struct": {}, "switch": {},
	"template": {}, "this": {}, "thread_local": {}, "throw": {}, "true": {},
	"try": {}, "typedef": {}, "typeid": {}, "typename": {}, "union": {},
	"unsigned": {}, "using": {}, "virtual": {}, "void": {}, "volatile": {},
	"wchar_t": {}, "while": {}, "xor": {}, "xor_eq": {},
	// Common competitive-programming library names we want to keep as
	// structural anchors. Preserving these makes k-grams reflect algorithm
	// shape, e.g. "for ( id = id ; id < id . size ( ) ; id ++ )".
	"std": {}, "cin": {}, "cout": {}, "cerr": {}, "endl": {}, "size": {},
	"push_back": {}, "pop_back": {}, "begin": {}, "end": {}, "front": {},
	"back": {}, "sort": {}, "reverse": {}, "swap": {}, "min": {}, "max": {},
	"abs": {}, "string": {}, "vector": {}, "map": {}, "set": {}, "pair": {},
	"make_pair": {}, "first": {}, "second": {}, "printf": {}, "scanf": {},
	// Our own literal placeholders produced during normalization.
	"str": {}, "chr": {},
}

// normalizeCpp strips comments, string/char literals, preprocessor lines, and
// `using namespace ...;` declarations so trivial cosmetic rewrites don't
// defeat the fingerprint.
func normalizeCpp(src string) string {
	s := src
	s = reBlockComment.ReplaceAllString(s, " ")
	s = reLineComment.ReplaceAllString(s, " ")
	s = reStringLit.ReplaceAllString(s, " STR ")
	s = reCharLit.ReplaceAllString(s, " CHR ")
	s = rePreproc.ReplaceAllString(s, " ")
	s = reUsingNS.ReplaceAllString(s, " ")
	return strings.ToLower(s)
}

// canonicalizeToken maps user-chosen identifiers to a generic "id" marker
// and numeric literals to "num" while leaving keywords, operators, and
// punctuation intact. This defeats rename-only and magic-number-tweak
// rewrites so the resulting fingerprint reflects code structure.
func canonicalizeToken(tok string) string {
	if reNumber.MatchString(tok) {
		return "num"
	}
	if reIdent.MatchString(tok) && reIdent.FindString(tok) == tok {
		if _, ok := cppKeywords[tok]; ok {
			return tok
		}
		return "id"
	}
	return tok
}

// cppFeatures tokenizes normalized C++ and returns overlapping 4-grams of
// *canonicalized* tokens as byte-slice features suitable for simhash.SimhashBytes.
func cppFeatures(src string) [][]byte {
	norm := normalizeCpp(src)
	raw := reToken.FindAllString(norm, -1)
	tokens := make([]string, len(raw))
	for i, t := range raw {
		tokens[i] = canonicalizeToken(t)
	}
	if len(tokens) < 4 {
		out := make([][]byte, 0, len(tokens))
		for _, t := range tokens {
			out = append(out, []byte(t))
		}
		return out
	}
	const k = 4
	count := len(tokens) - k + 1
	features := make([][]byte, count)
	for i := 0; i < count; i++ {
		features[i] = []byte(strings.Join(tokens[i:i+k], " "))
	}
	return features
}

// ──────────────────────────────────────────────────────────────
// Union-find
// ──────────────────────────────────────────────────────────────

type unionFind struct {
	parent map[string]string
	rank   map[string]int
}

func newUnionFind() *unionFind {
	return &unionFind{parent: map[string]string{}, rank: map[string]int{}}
}

func (u *unionFind) find(x string) string {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
		u.rank[x] = 0
		return x
	}
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]] // path compression (halving)
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if u.rank[ra] < u.rank[rb] {
		u.parent[ra] = rb
	} else if u.rank[ra] > u.rank[rb] {
		u.parent[rb] = ra
	} else {
		u.parent[rb] = ra
		u.rank[ra]++
	}
}
