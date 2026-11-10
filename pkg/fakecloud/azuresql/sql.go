// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package azuresql

// The statements: a small T-SQL interpreter over the fake's users and role
// memberships, for exactly the statements ASO's Azure SQL User code sends
// (pkg/util/azuresql). Anything else is refused with SQL Server's syntax
// error, so a change in what the operator sends shows up as a failure.

import (
	"fmt"
	"strings"
	"unicode"
)

type tokKind int

const (
	tWord tokKind = iota
	tIdent
	tString
	tParam
	tSymbol
)

type token struct {
	kind tokKind
	text string // a word upper-cased; an identifier or string unquoted
}

func tokenize(s string) ([]token, error) {
	var out []token
	r := []rune(s)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '-' && i+1 < len(r) && r[i+1] == '-':
			for i < len(r) && r[i] != '\n' {
				i++
			}
		case c == '[':
			var b strings.Builder
			i++
			for {
				if i >= len(r) {
					return nil, syntaxError("[")
				}
				if r[i] == ']' {
					if i+1 < len(r) && r[i+1] == ']' {
						b.WriteRune(']')
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteRune(r[i])
				i++
			}
			out = append(out, token{tIdent, b.String()})
		case c == '\'' || (c == 'N' && i+1 < len(r) && r[i+1] == '\''):
			if c == 'N' {
				i++
			}
			var b strings.Builder
			i++
			for {
				if i >= len(r) {
					return nil, syntaxError("'")
				}
				if r[i] == '\'' {
					if i+1 < len(r) && r[i+1] == '\'' {
						b.WriteRune('\'')
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteRune(r[i])
				i++
			}
			out = append(out, token{tString, b.String()})
		case c == '@':
			j := i + 1
			for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '_') {
				j++
			}
			out = append(out, token{tParam, string(r[i:j])})
			i = j
		case unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '#':
			j := i
			for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '_' || r[j] == '.' || r[j] == '#' || r[j] == '$') {
				j++
			}
			out = append(out, token{tWord, strings.ToUpper(string(r[i:j]))})
			i = j
		default:
			out = append(out, token{tSymbol, string(c)})
			i++
		}
	}
	return out, nil
}

func syntaxError(near string) *sqlError {
	return &sqlError{Number: 102, Class: 15, Msg: fmt.Sprintf("Incorrect syntax near '%s'.", near)}
}

func permissionError() *sqlError {
	return &sqlError{Number: 15247, Class: 16, Msg: "User does not have permission to perform this action."}
}

// result is one result set.
type result struct {
	columns []string
	rows    [][]string
	ints    bool
}

// session is a signed-in connection to one database.
type session struct {
	srv      *Server
	db       *database
	admin    bool
	entra    bool // signed in with an access token
	params   map[string]string
	toks     []token
	pos      int
	results  []*result
	affected uint64
}

func (s *session) peek() token {
	if s.pos < len(s.toks) {
		return s.toks[s.pos]
	}
	return token{kind: tSymbol, text: ""}
}

func (s *session) next() token {
	t := s.peek()
	s.pos++
	return t
}

func (s *session) word(w string) bool {
	t := s.peek()
	if t.kind == tWord && t.text == w {
		s.pos++
		return true
	}
	return false
}

func (s *session) symbol(c string) bool {
	t := s.peek()
	if t.kind == tSymbol && t.text == c {
		s.pos++
		return true
	}
	return false
}

func (s *session) expectWord(w string) error {
	if !s.word(w) {
		return syntaxError(s.peek().text)
	}
	return nil
}

func (s *session) expectSymbol(c string) error {
	if !s.symbol(c) {
		return syntaxError(s.peek().text)
	}
	return nil
}

func (s *session) ident() (string, error) {
	t := s.next()
	switch t.kind {
	case tIdent:
		return t.text, nil
	case tWord:
		return strings.ToLower(t.text), nil
	}
	return "", syntaxError(t.text)
}

// value is a string literal or a parameter.
func (s *session) value() (string, error) {
	t := s.next()
	switch t.kind {
	case tString:
		return t.text, nil
	case tParam:
		for k, v := range s.params {
			if strings.EqualFold(k, t.text) {
				return v, nil
			}
		}
		return "", &sqlError{Number: 137, Class: 15, Msg: fmt.Sprintf("Must declare the scalar variable \"%s\".", t.text)}
	}
	return "", syntaxError(t.text)
}

// run executes a batch.
func (s *session) run(text string, params map[string]string) error {
	toks, err := tokenize(text)
	if err != nil {
		return err
	}
	s.toks, s.pos, s.params = toks, 0, params
	for s.pos < len(s.toks) {
		if s.symbol(";") {
			continue
		}
		if err := s.statement(true); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) statement(execute bool) error {
	switch {
	case s.word("SELECT"):
		return s.selectStatement(execute)
	case s.word("IF"):
		return s.ifStatement(execute)
	case s.word("BEGIN"):
		for !s.word("END") {
			if s.pos >= len(s.toks) {
				return syntaxError("BEGIN")
			}
			if s.symbol(";") {
				continue
			}
			if err := s.statement(execute); err != nil {
				return err
			}
		}
		return nil
	case s.word("CREATE"):
		return s.createUser(execute)
	case s.word("ALTER"):
		if s.word("USER") {
			return s.alterUser(execute)
		}
		if s.word("ROLE") {
			return s.alterRole(execute)
		}
	case s.word("DROP"):
		return s.dropUser(execute)
	}
	return syntaxError(s.peek().text)
}

// existsQuery reads "SELECT name FROM sysusers WHERE name = <value>".
func (s *session) existsQuery() (bool, error) {
	if err := s.expectWord("SELECT"); err != nil {
		return false, err
	}
	if !s.word("NAME") && !s.symbol("*") {
		return false, syntaxError(s.peek().text)
	}
	for _, w := range []string{"FROM", "SYSUSERS", "WHERE", "NAME"} {
		if err := s.expectWord(w); err != nil {
			return false, err
		}
	}
	if err := s.expectSymbol("="); err != nil {
		return false, err
	}
	name, err := s.value()
	if err != nil {
		return false, err
	}
	return s.db.principalExists(name), nil
}

func (s *session) selectStatement(execute bool) error {
	switch {
	case s.peek().kind == tWord && s.peek().text == "1":
		s.pos++
		if execute {
			s.results = append(s.results, &result{columns: []string{""}, rows: [][]string{{"1"}}, ints: true})
		}
		return nil
	case s.symbol("*"):
		for _, w := range []string{"FROM", "SYSUSERS", "WHERE", "NAME"} {
			if err := s.expectWord(w); err != nil {
				return err
			}
		}
		if err := s.expectSymbol("="); err != nil {
			return err
		}
		name, err := s.value()
		if err != nil {
			return err
		}
		if execute {
			r := &result{columns: []string{"name"}}
			if n, ok := s.db.principalName(name); ok {
				r.rows = append(r.rows, []string{n})
			}
			s.results = append(s.results, r)
		}
		return nil
	case s.word("R.NAME"):
		// SELECT r.name role_principal_name FROM sys.database_role_members rm
		// JOIN sys.database_principals r ON ... JOIN sys.database_principals m
		// ON ... where m.name = <value>: ASO's GetUserRoles.
		start := s.pos
		for s.pos < len(s.toks) && !(s.peek().kind == tWord && s.peek().text == "M.NAME") {
			s.pos++
		}
		if !strings.Contains(fmt.Sprint(s.toks[start:s.pos]), "SYS.DATABASE_ROLE_MEMBERS") {
			return syntaxError("r.name")
		}
		if err := s.expectWord("M.NAME"); err != nil {
			return err
		}
		if err := s.expectSymbol("="); err != nil {
			return err
		}
		name, err := s.value()
		if err != nil {
			return err
		}
		if execute {
			r := &result{columns: []string{"role_principal_name"}}
			for _, role := range s.db.rolesOf(name) {
				r.rows = append(r.rows, []string{role})
			}
			s.results = append(s.results, r)
		}
		return nil
	}
	return syntaxError(s.peek().text)
}

func (s *session) ifStatement(execute bool) error {
	if err := s.expectWord("NOT"); err != nil {
		return err
	}
	if err := s.expectWord("EXISTS"); err != nil {
		return err
	}
	if err := s.expectSymbol("("); err != nil {
		return err
	}
	exists, err := s.existsQuery()
	if err != nil {
		return err
	}
	if err := s.expectSymbol(")"); err != nil {
		return err
	}
	if err := s.statement(execute && !exists); err != nil {
		return err
	}
	for s.symbol(";") {
	}
	if s.word("ELSE") {
		return s.statement(execute && exists)
	}
	return nil
}

func (s *session) createUser(execute bool) error {
	if err := s.expectWord("USER"); err != nil {
		return err
	}
	name, err := s.ident()
	if err != nil {
		return err
	}
	switch {
	case s.word("WITH"):
		if err := s.expectWord("PASSWORD"); err != nil {
			return err
		}
		if err := s.expectSymbol("="); err != nil {
			return err
		}
		password, err := s.value()
		if err != nil {
			return err
		}
		if !execute {
			return nil
		}
		if !s.admin {
			return permissionError()
		}
		return s.db.createUser(name, password, false)
	case s.word("FROM"):
		if err := s.expectWord("EXTERNAL"); err != nil {
			return err
		}
		if err := s.expectWord("PROVIDER"); err != nil {
			return err
		}
		if !execute {
			return nil
		}
		if !s.admin {
			return permissionError()
		}
		if !s.entra {
			return &sqlError{Number: 33159, Class: 16, Msg: fmt.Sprintf("Principal '%s' could not be created. Only connections established with Active Directory accounts can create other Active Directory users.", name)}
		}
		if !s.srv.inDirectory(name) {
			return &sqlError{Number: 33130, Class: 16, Msg: fmt.Sprintf("Principal '%s' could not be found or this principal type is not supported.", name)}
		}
		return s.db.createUser(name, "", true)
	}
	return syntaxError(s.peek().text)
}

func (s *session) alterUser(execute bool) error {
	name, err := s.ident()
	if err != nil {
		return err
	}
	for _, w := range []string{"WITH", "PASSWORD"} {
		if err := s.expectWord(w); err != nil {
			return err
		}
	}
	if err := s.expectSymbol("="); err != nil {
		return err
	}
	password, err := s.value()
	if err != nil {
		return err
	}
	if !execute {
		return nil
	}
	if !s.admin {
		return permissionError()
	}
	return s.db.setPassword(name, password)
}

func (s *session) dropUser(execute bool) error {
	if err := s.expectWord("USER"); err != nil {
		return err
	}
	ifExists := false
	if s.word("IF") {
		if err := s.expectWord("EXISTS"); err != nil {
			return err
		}
		ifExists = true
	}
	name, err := s.ident()
	if err != nil {
		return err
	}
	if !execute {
		return nil
	}
	if !s.admin {
		return permissionError()
	}
	return s.db.dropUser(name, ifExists)
}

func (s *session) alterRole(execute bool) error {
	role, err := s.ident()
	if err != nil {
		return err
	}
	add := s.word("ADD")
	if !add && !s.word("DROP") {
		return syntaxError(s.peek().text)
	}
	if err := s.expectWord("MEMBER"); err != nil {
		return err
	}
	member, err := s.ident()
	if err != nil {
		return err
	}
	if !execute {
		return nil
	}
	if !s.admin {
		return permissionError()
	}
	return s.db.alterRole(role, member, add)
}
