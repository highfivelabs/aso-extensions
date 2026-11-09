// Copyright 2026 High Five Labs
// SPDX-License-Identifier: MIT

package postgresql

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/highfivelabs/aso-extensions/pkg/util/set"
)

func TestNormalizeSchemaPrivileges(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)

	got, err := NormalizeSchemaPrivileges([]string{"usage", " CREATE "})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(set.AsSortedSlice(got)).To(Equal([]string{"CREATE", "USAGE"}))

	got, err = NormalizeSchemaPrivileges([]string{"ALL PRIVILEGES"})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(set.AsSortedSlice(got)).To(Equal([]string{"CREATE", "USAGE"}))

	_, err = NormalizeSchemaPrivileges([]string{"SELECT"})
	g.Expect(err).To(MatchError(ContainSubstring(`"SELECT" is not a schema privilege`)))
}

func TestNormalizeDefaultPrivileges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		objectType string
		privileges []string
		version    int
		want       []string
		wantErr    string
	}{
		{name: "tables ALL before 17", objectType: "tables", privileges: []string{"ALL"}, version: 160004,
			want: []string{"DELETE", "INSERT", "REFERENCES", "SELECT", "TRIGGER", "TRUNCATE", "UPDATE"}},
		{name: "tables ALL from 17 adds MAINTAIN", objectType: "tables", privileges: []string{"all"}, version: 170011,
			want: []string{"DELETE", "INSERT", "MAINTAIN", "REFERENCES", "SELECT", "TRIGGER", "TRUNCATE", "UPDATE"}},
		{name: "MAINTAIN before 17 is refused", objectType: "tables", privileges: []string{"MAINTAIN"}, version: 160004,
			wantErr: `"MAINTAIN" is not a privilege on tables`},
		{name: "sequences", objectType: "sequences", privileges: []string{"usage", "SELECT"}, version: 170011,
			want: []string{"SELECT", "USAGE"}},
		{name: "functions", objectType: "functions", privileges: []string{"ALL PRIVILEGES"}, version: 170011,
			want: []string{"EXECUTE"}},
		{name: "types", objectType: "types", privileges: []string{"USAGE"}, version: 170011,
			want: []string{"USAGE"}},
		{name: "a privilege of another object type", objectType: "functions", privileges: []string{"SELECT"}, version: 170011,
			wantErr: `"SELECT" is not a privilege on functions`},
		{name: "an unknown object type", objectType: "schemas", privileges: []string{"USAGE"}, version: 170011,
			wantErr: `"schemas" is not an object type`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			got, err := NormalizeDefaultPrivileges(c.objectType, c.privileges, c.version)
			if c.wantErr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(c.wantErr)))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(set.AsSortedSlice(got)).To(Equal(c.want))
		})
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)

	add, remove := diff(set.Make("USAGE", "CREATE"), set.Make("USAGE", "SELECT", "INSERT"))
	g.Expect(add).To(Equal([]string{"INSERT", "SELECT"}))
	g.Expect(remove).To(Equal([]string{"CREATE"}))

	add, remove = diff(nil, nil)
	g.Expect(add).To(BeEmpty())
	g.Expect(remove).To(BeEmpty())
}
