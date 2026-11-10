// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

// Package azuresql is a fake Azure SQL Database server for the Azure SQL
// User kind, for tests and test builds only. It speaks TDS 7.4 over TLS
// (tds.go), so go-mssqldb, the driver ASO's code uses, runs unchanged
// against it, and it keeps the database users and role memberships those
// statements change (sql.go).
//
// No SQL Server runs here: SQL Server outside Azure has no Microsoft Entra
// sign-in and refuses CREATE USER ... FROM EXTERNAL PROVIDER, so a real one
// could not show the Entra path at all. What the fake models, from Azure
// SQL's documentation
// (https://learn.microsoft.com/azure/azure-sql/database/authentication-aad-configure):
//   - sign-in with the server's SQL administrator password, a contained
//     user's password, or an access token: the token signs in as the Entra
//     principal Principal names, which must be the server's Entra
//     administrator or a user created FROM EXTERNAL PROVIDER in the database;
//   - only an administrator creates, changes or drops users and role
//     memberships;
//   - only a connection signed in with an access token may create a user
//     FROM EXTERNAL PROVIDER (error 33159), and only for a name the
//     directory knows (error 33130);
//   - names compare without regard to case (Azure SQL's default collation);
//   - every database has the fixed roles (db_owner, db_datareader, ...).
package azuresql

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
)

// FixedRoles are the fixed database roles every database has.
var FixedRoles = []string{
	"db_owner", "db_securityadmin", "db_accessadmin", "db_backupoperator",
	"db_ddladmin", "db_datawriter", "db_datareader", "db_denydatawriter", "db_denydatareader",
}

// Server is a fake Azure SQL logical server.
type Server struct {
	// Name is the server's name, reported in errors.
	Name string
	// TLS holds the server's certificate.
	TLS *tls.Config
	// Admin and AdminPassword: the server's SQL administrator.
	Admin, AdminPassword string
	// EntraAdmin is the name of the server's Microsoft Entra administrator.
	EntraAdmin string
	// Principal returns the Entra principal an access token signs in as,
	// or "" to refuse the token.
	Principal func(token string) string
	// Directory lists the Entra principals (users, groups, applications
	// and managed identities, by display name) CREATE USER ... FROM
	// EXTERNAL PROVIDER may name.
	Directory []string
	// Databases exist from the start.
	Databases []string
	// Log, if set, receives one line per sign-in and statement.
	Log *log.Logger

	mu       sync.Mutex
	dbs      map[string]*database
	listener net.Listener
}

// User is a database user as the fake holds it.
type User struct {
	Name     string
	External bool // created FROM EXTERNAL PROVIDER
	Roles    []string
}

type database struct {
	name  string
	users map[string]*dbUser // by lower-cased name
	roles map[string]string  // lower-cased name to name
}

type dbUser struct {
	name     string
	password string
	external bool
	roles    map[string]bool // lower-cased role names
}

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Printf(format, args...)
	}
}

func (s *Server) init() {
	if s.dbs != nil {
		return
	}
	s.dbs = map[string]*database{}
	for _, name := range s.Databases {
		d := &database{name: name, users: map[string]*dbUser{}, roles: map[string]string{}}
		for _, r := range FixedRoles {
			d.roles[r] = r
		}
		s.dbs[strings.ToLower(name)] = d
	}
}

// Users lists a database's users, sorted by name.
func (s *Server) Users(db string) []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	d := s.dbs[strings.ToLower(db)]
	if d == nil {
		return nil
	}
	var out []User
	for _, u := range d.users {
		var roles []string
		for r := range u.roles {
			roles = append(roles, d.roles[r])
		}
		sort.Strings(roles)
		out = append(out, User{Name: u.name, External: u.external, Roles: roles})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) inDirectory(name string) bool {
	for _, n := range s.Directory {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

func (d *database) principalName(name string) (string, bool) {
	if u, ok := d.users[strings.ToLower(name)]; ok {
		return u.name, true
	}
	if r, ok := d.roles[strings.ToLower(name)]; ok {
		return r, true
	}
	return "", false
}

func (d *database) principalExists(name string) bool {
	_, ok := d.principalName(name)
	return ok
}

func (d *database) createUser(name, password string, external bool) error {
	if d.principalExists(name) {
		return &sqlError{Number: 15023, Class: 16, Msg: fmt.Sprintf("User, group, or role '%s' already exists in the current database.", name)}
	}
	d.users[strings.ToLower(name)] = &dbUser{name: name, password: password, external: external, roles: map[string]bool{}}
	return nil
}

func (d *database) setPassword(name, password string) error {
	u, ok := d.users[strings.ToLower(name)]
	if !ok {
		return &sqlError{Number: 15151, Class: 16, Msg: fmt.Sprintf("Cannot alter the user '%s', because it does not exist or you do not have permission.", name)}
	}
	if u.external {
		return &sqlError{Number: 15537, Class: 16, Msg: "Cannot set a password for a user created from an external provider."}
	}
	u.password = password
	return nil
}

func (d *database) dropUser(name string, ifExists bool) error {
	if _, ok := d.users[strings.ToLower(name)]; !ok {
		if ifExists {
			return nil
		}
		return &sqlError{Number: 15151, Class: 16, Msg: fmt.Sprintf("Cannot drop the user '%s', because it does not exist or you do not have permission.", name)}
	}
	delete(d.users, strings.ToLower(name))
	return nil
}

func (d *database) alterRole(role, member string, add bool) error {
	if _, ok := d.roles[strings.ToLower(role)]; !ok {
		return &sqlError{Number: 15151, Class: 16, Msg: fmt.Sprintf("Cannot alter the role '%s', because it does not exist or you do not have permission.", role)}
	}
	u, ok := d.users[strings.ToLower(member)]
	if !ok {
		return &sqlError{Number: 15151, Class: 16, Msg: fmt.Sprintf("Cannot add the principal '%s', because it does not exist or you do not have permission.", member)}
	}
	if add {
		u.roles[strings.ToLower(role)] = true
	} else {
		delete(u.roles, strings.ToLower(role))
	}
	return nil
}

func (d *database) rolesOf(name string) []string {
	u, ok := d.users[strings.ToLower(name)]
	if !ok {
		return nil
	}
	var out []string
	for r := range u.roles {
		out = append(out, d.roles[r])
	}
	sort.Strings(out)
	return out
}

// Listen serves on addr (host:port; port 0 picks one) in the background
// and returns the address it listens on.
func (s *Server) Listen(addr string) (net.Addr, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.listener = l
	s.init()
	s.mu.Unlock()
	go func() { _ = s.Serve(l) }()
	return l.Addr(), nil
}

// Serve accepts connections on l until it is closed.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	s.init()
	s.mu.Unlock()
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer c.Close()
			if err := s.serveConn(c); err != nil && !errors.Is(err, io.EOF) {
				s.logf("connection from %s: %v", c.RemoteAddr(), err)
			}
		}()
	}
}

// Close stops listening.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

func (s *Server) serveConn(c net.Conn) error {
	typ, payload, err := readMessage(c)
	if err != nil {
		return err
	}
	if typ != packPrelogin {
		return fmt.Errorf("first packet %#x, want PRELOGIN", typ)
	}
	fields, err := parsePrelogin(payload)
	if err != nil {
		return err
	}
	if enc, ok := fields[preloginEncryption]; ok && len(enc) == 1 && enc[0] == 2 {
		// The client cannot encrypt; Azure SQL refuses it.
		return errors.New("client does not support encryption")
	}
	if err := writeMessage(c, packReply, preloginReply(fields)); err != nil {
		return err
	}
	hs := &handshakeConn{rw: c}
	cfg := s.TLS.Clone()
	cfg.MaxVersion = tls.VersionTLS12 // TDS 7.4 carries the handshake in packets; keep it to one round of flights
	cfg.SessionTicketsDisabled = true
	tc := tls.Server(&connAdapter{Conn: c, rw: hs}, cfg)
	if err := tc.Handshake(); err != nil {
		return fmt.Errorf("TLS handshake: %w", err)
	}
	hs.finished = true

	typ, payload, err = readMessage(tc)
	if err != nil {
		return err
	}
	if typ != packLogin7 {
		return fmt.Errorf("packet %#x after the handshake, want LOGIN7", typ)
	}
	l, err := parseLogin7(payload)
	if err != nil {
		return err
	}
	sess, lerr := s.signIn(l)
	var t tokens
	if lerr != nil {
		s.logf("sign-in to %q refused: %v", l.database, lerr)
		t.errorToken(lerr, s.Name)
		t.done(tokDone, doneError, 0)
		return writeMessage(tc, packReply, t.Bytes())
	}
	t.databaseChange(sess.db.name)
	t.loginAck()
	if l.hasToken {
		t.fedAuthAck()
	}
	t.done(tokDone, 0, 0)
	if err := writeMessage(tc, packReply, t.Bytes()); err != nil {
		return err
	}

	for {
		typ, payload, err := readMessage(tc)
		if err != nil {
			return err
		}
		var reply tokens
		switch typ {
		case packSQLBatch:
			text, err := batchText(payload)
			if err != nil {
				return err
			}
			s.execute(sess, text, nil, &reply, false)
		case packRPC:
			text, params, err := rpcCall(payload)
			if err != nil {
				var se *sqlError
				if !errors.As(err, &se) {
					return err
				}
				reply.errorToken(se, s.Name)
				reply.done(tokDoneProc, doneError, 0)
				break
			}
			s.execute(sess, text, params, &reply, true)
		case packAttention:
			reply.done(tokDone, doneAttn, 0)
		default:
			return fmt.Errorf("unexpected packet type %#x", typ)
		}
		if err := writeMessage(tc, packReply, reply.Bytes()); err != nil {
			return err
		}
	}
}

// connAdapter is a net.Conn whose reads and writes go through rw (the
// handshake framing), for tls.Server.
type connAdapter struct {
	net.Conn
	rw io.ReadWriter
}

func (c *connAdapter) Read(b []byte) (int, error)  { return c.rw.Read(b) }
func (c *connAdapter) Write(b []byte) (int, error) { return c.rw.Write(b) }

func (s *Server) signIn(l login) (*session, *sqlError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db := l.database
	if db == "" {
		db = "master"
	}
	d := s.dbs[strings.ToLower(db)]
	if l.hasToken {
		who := ""
		if s.Principal != nil {
			who = s.Principal(l.token)
		}
		if who == "" {
			return nil, &sqlError{Number: 18456, Class: 14, Msg: "Login failed for user '<token-identified principal>'."}
		}
		if d == nil {
			return nil, &sqlError{Number: 4060, Class: 11, Msg: fmt.Sprintf("Cannot open database \"%s\" requested by the login. The login failed.", db)}
		}
		if strings.EqualFold(who, s.EntraAdmin) {
			s.logf("%s signed in to %s with a token, as the Entra administrator", who, d.name)
			return &session{srv: s, db: d, admin: true, entra: true}, nil
		}
		if u, ok := d.users[strings.ToLower(who)]; ok && u.external {
			s.logf("%s signed in to %s with a token", who, d.name)
			return &session{srv: s, db: d, entra: true}, nil
		}
		return nil, &sqlError{Number: 18456, Class: 14, Msg: fmt.Sprintf("Login failed for user '%s'.", who)}
	}
	if d == nil {
		return nil, &sqlError{Number: 4060, Class: 11, Msg: fmt.Sprintf("Cannot open database \"%s\" requested by the login. The login failed.", db)}
	}
	if strings.EqualFold(l.user, s.Admin) && l.password == s.AdminPassword {
		s.logf("%s signed in to %s with a password, as the SQL administrator", l.user, d.name)
		return &session{srv: s, db: d, admin: true}, nil
	}
	if u, ok := d.users[strings.ToLower(l.user)]; ok && !u.external && u.password == l.password {
		s.logf("%s signed in to %s with a password", l.user, d.name)
		return &session{srv: s, db: d}, nil
	}
	return nil, &sqlError{Number: 18456, Class: 14, Msg: fmt.Sprintf("Login failed for user '%s'.", l.user)}
}

// execute runs a batch or an sp_executesql call and writes its reply.
func (s *Server) execute(sess *session, text string, params map[string]string, reply *tokens, rpc bool) {
	s.mu.Lock()
	sess.results = nil
	err := sess.run(text, params)
	results := sess.results
	s.mu.Unlock()
	s.logf("%s: %s (err %v)", sess.db.name, strings.Join(strings.Fields(text), " "), err)

	doneTok := byte(tokDone)
	if rpc {
		doneTok = tokDoneInProc
	}
	for _, r := range results {
		reply.resultSet(r)
		reply.done(doneTok, doneMore|doneCount, uint64(len(r.rows)))
	}
	var se *sqlError
	if err != nil {
		if !errors.As(err, &se) {
			se = &sqlError{Number: 50000, Class: 16, Msg: err.Error()}
		}
		reply.errorToken(se, s.Name)
	}
	status := uint16(0)
	if se != nil {
		status = doneError
	}
	if rpc {
		reply.u8(tokReturnStatus)
		reply.u32(0)
		reply.done(tokDoneProc, status, 0)
		return
	}
	reply.done(tokDone, status, 0)
}
