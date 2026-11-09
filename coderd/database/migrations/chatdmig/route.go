package chatdmig

import (
	"regexp"
	"strings"
)

var (
	ownedRelation = regexp.MustCompile(`^(chats|chats_.+|chat_.+|skills|mcp_server_(configs|user_tokens)|organization_member_(disabled_skills|skill_settings)|user_ai_provider_keys)$`)
	ownedFunction = regexp.MustCompile(`chat|skill|mcp_server`)

	createRe = regexp.MustCompile(`(?is)\bCREATE\s+(?:OR\s+REPLACE\s+)?(?:UNLOGGED\s+)?(TABLE|MATERIALIZED\s+VIEW|VIEW|TYPE|FUNCTION|PROCEDURE|SEQUENCE|DOMAIN)\s+(?:IF\s+NOT\s+EXISTS\s+)?([\w."]+)`)
	touchRe  = regexp.MustCompile(`(?is)\b(?:ALTER\s+TABLE(?:\s+ONLY)?(?:\s+IF\s+EXISTS)?|UPDATE|DELETE\s+FROM|INSERT\s+INTO|ON|FROM|JOIN|REFERENCES|DROP\s+(?:TABLE|VIEW)(?:\s+IF\s+EXISTS)?|TRUNCATE(?:\s+TABLE)?|COMMENT\s+ON\s+(?:TABLE|COLUMN))\s+([\w."]+)`)
	typeRe   = regexp.MustCompile(`(?is)\b(?:ALTER|DROP)\s+TYPE(?:\s+IF\s+EXISTS)?\s+([\w."]+)`)
)

// Classifier picks the ledger of an upstream migration from the names that
// it creates. Only an unqualified CREATE depends on the search path, so the
// created names decide. Relations lists the tables and views of the upstream
// dump, so that words such as "ON CONFLICT" do not count as tables.
type Classifier struct {
	Relations map[string]bool
}

var dumpRelation = regexp.MustCompile(`(?m)^CREATE (?:TABLE|VIEW|MATERIALIZED VIEW) (?:public\.)?(\w+)`)

// NewClassifier reads the relation names from the text of dump.sql.
func NewClassifier(dump string) *Classifier {
	c := &Classifier{Relations: map[string]bool{}}
	for _, m := range dumpRelation.FindAllStringSubmatch(dump, -1) {
		c.Relations[m[1]] = true
	}
	return c
}

func owned(kind, name string) bool {
	switch kind {
	case "function", "procedure":
		return ownedFunction.MatchString(name)
	case "type", "domain":
		return strings.HasPrefix(name, "chat_")
	case "sequence":
		return strings.HasPrefix(name, "chat_") || ownedRelation.MatchString(name)
	default:
		return ownedRelation.MatchString(name)
	}
}

// Classify returns LedgerChatd if the migration creates only chatd objects,
// or creates nothing and touches only chatd objects. It returns LedgerMixed
// if it creates objects of both kinds, and LedgerCore otherwise.
func (c *Classifier) Classify(sql string) Ledger {
	sql = stripLiterals(sql)
	creates := createRe.FindAllStringSubmatch(sql, -1)
	if len(creates) > 0 {
		chatd, core := 0, 0
		for _, m := range creates {
			kind := strings.ToLower(strings.Join(strings.Fields(m[1]), " "))
			if owned(kind, bareName(m[2])) {
				chatd++
			} else {
				core++
			}
		}
		switch {
		case core == 0:
			return LedgerChatd
		case chatd > 0:
			return LedgerMixed
		default:
			return LedgerCore
		}
	}

	touched := 0
	for _, m := range touchRe.FindAllStringSubmatch(sql, -1) {
		name := bareName(m[1])
		if !c.Relations[name] && !ownedRelation.MatchString(name) {
			continue
		}
		if !owned("table", name) {
			return LedgerCore
		}
		touched++
	}
	for _, m := range typeRe.FindAllStringSubmatch(sql, -1) {
		if !owned("type", bareName(m[1])) {
			return LedgerCore
		}
		touched++
	}
	if touched > 0 {
		return LedgerChatd
	}
	return LedgerCore
}

func bareName(n string) string {
	n = strings.ToLower(strings.ReplaceAll(n, `"`, ""))
	if i := strings.LastIndex(n, "."); i >= 0 {
		n = n[i+1:]
	}
	return n
}

var dollarTag = regexp.MustCompile(`^\$[A-Za-z_]*\$`)

// stripLiterals removes comments, quoted strings and dollar-quoted bodies,
// so that names inside a function body or a comment do not count.
func stripLiterals(sql string) string {
	var b strings.Builder
	for i := 0; i < len(sql); {
		switch {
		case strings.HasPrefix(sql[i:], "--"):
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				return b.String()
			}
			i += j
		case strings.HasPrefix(sql[i:], "/*"):
			j := strings.Index(sql[i+2:], "*/")
			if j < 0 {
				return b.String()
			}
			i += j + 4
		case sql[i] == '\'':
			j := i + 1
			for j < len(sql) {
				if sql[j] == '\'' {
					if j+1 < len(sql) && sql[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			_, _ = b.WriteString("''")
			i = j + 1
		case sql[i] == '$':
			tag := dollarTag.FindString(sql[i:])
			if tag == "" {
				_ = b.WriteByte(sql[i])
				i++
				continue
			}
			j := strings.Index(sql[i+len(tag):], tag)
			if j < 0 {
				return b.String()
			}
			_, _ = b.WriteString(" '' ")
			i += len(tag) + j + len(tag)
		default:
			_ = b.WriteByte(sql[i])
			i++
		}
	}
	return b.String()
}
