package scanner_test

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/conf/configtest"
	"github.com/navidrome/navidrome/core/artwork"
	"github.com/navidrome/navidrome/core/metrics"
	"github.com/navidrome/navidrome/core/playlists"
	"github.com/navidrome/navidrome/db"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/request"
	"github.com/navidrome/navidrome/persistence"
	"github.com/navidrome/navidrome/scanner"
	"github.com/navidrome/navidrome/server/events"
	"github.com/navidrome/navidrome/tests"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Issue #6276: scans a real library folder on the native filesystem, so on Windows the
// folder paths go through the OS path handling, and checks what reaches the DB in phase 1
// (folder.num_playlists) and phase 4 (imported playlists).
var _ = Describe("Scanner - PlaylistsPath on the native filesystem", Ordered, ContinueOnFailure, Label("issue-6276"), func() {
	var ctx context.Context
	var ds model.DataStore
	var s model.Scanner
	var libPath string

	BeforeAll(func() {
		ctx = request.WithUser(GinkgoT().Context(), model.User{ID: "123", IsAdmin: true})
		// The DB stays open until the suite ends, and Windows can't delete an open file
		tmpDir, err := os.MkdirTemp("", "scanner-playlists-path-test")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = os.RemoveAll(tmpDir) })
		conf.Server.DbPath = filepath.Join(tmpDir, "test-scanner.db?_journal_mode=WAL")
		db.Db().SetMaxOpenConns(1)
	})

	BeforeEach(func() {
		DeferCleanup(configtest.SetupConfig())
		libPath = GinkgoT().TempDir()
		conf.Server.MusicFolder = libPath
		conf.Server.DevExternalScanner = false
		conf.Server.AutoImportPlaylists = true

		writeNSP := func(relPath, name string) {
			GinkgoHelper()
			full := filepath.Join(libPath, filepath.FromSlash(relPath))
			Expect(os.MkdirAll(filepath.Dir(full), 0755)).To(Succeed())
			nsp := `{"name": "` + name + `", "all": [{"is": {"loved": true}}]}`
			Expect(os.WriteFile(full, []byte(nsp), 0600)).To(Succeed())
		}
		writeNSP("Root.nsp", "Root")
		writeNSP("Playlists/navidrome/Rock.nsp", "Rock")
		writeNSP("Playlists/navidrome/Deep/Nested.nsp", "Nested")
		writeNSP("Playlists/other/Other.nsp", "Other")

		db.Init(ctx)
		DeferCleanup(func() {
			Expect(tests.ClearDB()).To(Succeed())
		})
		ds = persistence.New(db.Db())

		adminUser := model.User{ID: "123", UserName: "admin", Name: "Admin User", IsAdmin: true, NewPassword: "password"}
		Expect(ds.User().Put(ctx, &adminUser)).To(Succeed())

		lib := model.Library{ID: 1, Name: "Native Library", Path: libPath}
		Expect(ds.Library().Put(ctx, &lib)).To(Succeed())

		s = scanner.New(ctx, ds, events.NoopBroker(),
			playlists.NewPlaylists(ds, artwork.NewUploader(ds)), metrics.NewNoopInstance())
	})

	foldersWithPlaylists := func() []string {
		GinkgoHelper()
		folders, err := ds.Folder().GetAll(ctx)
		Expect(err).ToNot(HaveOccurred())
		var paths []string
		for _, f := range folders {
			if f.NumPlaylists > 0 {
				paths = append(paths, path.Join(f.Path, f.Name))
			}
		}
		return paths
	}

	importedPlaylists := func() []string {
		GinkgoHelper()
		all, err := ds.Playlist().GetAll(ctx)
		Expect(err).ToNot(HaveOccurred())
		var names []string
		for _, p := range all {
			Expect(filepath.IsAbs(p.Path)).To(BeTrue(), "playlist path should be absolute: %s", p.Path)
			Expect(p.Path).To(HavePrefix(libPath))
			names = append(names, p.Name)
		}
		return names
	}

	DescribeTable("imports only playlists inside PlaylistsPath",
		func(pattern string, expectedFolders, expectedPlaylists []string) {
			conf.Server.PlaylistsPath = pattern

			_, err := s.ScanAll(ctx, true)
			Expect(err).ToNot(HaveOccurred())

			// One assertion, so a failure shows both the phase 1 and the phase 4 results
			Expect(map[string][]string{
				"phase 1: folders stored with num_playlists > 0": sorted(foldersWithPlaylists()),
				"phase 4: imported playlists":                    sorted(importedPlaylists()),
			}).To(Equal(map[string][]string{
				"phase 1: folders stored with num_playlists > 0": sorted(expectedFolders),
				"phase 4: imported playlists":                    sorted(expectedPlaylists),
			}))
		},
		Entry("empty (default) imports everything", "",
			[]string{".", "Playlists/navidrome", "Playlists/navidrome/Deep", "Playlists/other"},
			[]string{"Root", "Rock", "Nested", "Other"}),
		Entry("nested folder", "Playlists/navidrome",
			[]string{"Playlists/navidrome"},
			[]string{"Rock"}),
		Entry("root and a nested ** pattern", "."+string(filepath.ListSeparator)+"Playlists/navidrome/**",
			[]string{".", "Playlists/navidrome", "Playlists/navidrome/Deep"},
			[]string{"Root", "Rock", "Nested"}),
		Entry("non-matching pattern imports nothing", "Music/**",
			[]string{},
			[]string{}),
		// The config from the issue. Backslash is a separator on Windows, an escape elsewhere.
		Entry(`backslash nested folder (issue config)`, `Playlists\navidrome`,
			onWindows([]string{"Playlists/navidrome"}),
			onWindows([]string{"Rock"})),
	)
})

func sorted(values []string) []string {
	return slices.Sorted(slices.Values(values))
}

// onWindows returns values when running on Windows, and an empty slice everywhere else.
func onWindows(values []string) []string {
	if runtime.GOOS == "windows" {
		return values
	}
	return []string{}
}
