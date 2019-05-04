package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/brandur/modulr"
	"github.com/brandur/modulr/log"
	"github.com/brandur/modulr/mod/mace"
	"github.com/brandur/modulr/mod/mfile"
	"github.com/brandur/modulr/mod/mmarkdown"
	"github.com/brandur/modulr/mod/mtoc"
	"github.com/brandur/modulr/mod/myaml"
	"github.com/brandur/sorg/assets"
	"github.com/brandur/sorg/atom"
	"github.com/brandur/sorg/markdown"
	t "github.com/brandur/sorg/talks"
	"github.com/brandur/sorg/templatehelpers"
	"github.com/joeshaw/envdecode"
	"github.com/pkg/errors"
	"github.com/yosssi/ace"
	"gopkg.in/russross/blackfriday.v2"
)

//////////////////////////////////////////////////////////////////////////////
//
//
//
// Main
//
//
//
//////////////////////////////////////////////////////////////////////////////

func main() {
	config := &modulr.Config{
		Log:  &log.Logger{Level: log.LevelInfo},
		Port: "5004",
	}
	modulr.BuildLoop(config, build)
}

//////////////////////////////////////////////////////////////////////////////
//
//
//
// Constants
//
//
//
//////////////////////////////////////////////////////////////////////////////

const (
	// AbsoluteURL is the site's absolute URL. It's usually preferable that
	// it's not used, but it is when generating emails.
	AbsoluteURL = "https://brandur.org"

	// LayoutsDir is the source directory for view layouts.
	LayoutsDir = "./layouts"

	// MainLayout is the site's main layout.
	MainLayout = LayoutsDir + "/main"

	// PassageLayout is the layout for a Passages & Glass issue (an email
	// newsletter).
	PassageLayout = LayoutsDir + "/passages"

	// Release is the asset version of the site. Bump when any assets are
	// updated to blow away any browser caches.
	Release = "74"

	// TempDir is a temporary directory used to download images that will be
	// processed and such.
	TempDir = "./tmp"

	// ViewsDir is the source directory for views.
	ViewsDir = "./views"
)

// A set of tag constants to hopefully help ensure that this set doesn't grow
// very much.
const (
	tagPostgres Tag = "postgres"
)

// twitterInfo is some HTML that includes a Twitter link which can be appended
// to the publishing info of various content.
const twitterInfo = `<p>Find me on Twitter at ` +
	`<strong><a href="https://twitter.com/brandur">@brandur</a></strong>.</p>`

//////////////////////////////////////////////////////////////////////////////
//
//
//
// Variables
//
//
//
//////////////////////////////////////////////////////////////////////////////

// Left as a global for now for the sake of convenience, but it's not used in
// very many places and can probably be refactored as a local if desired.
var conf Conf

var renderComplexMarkdown func(string, *markdown.RenderOptions) string

//////////////////////////////////////////////////////////////////////////////
//
//
//
// Init
//
//
//
//////////////////////////////////////////////////////////////////////////////

// init runs on package initialization.
func init() {
	renderComplexMarkdown = markdown.ComposeRenderStack(func(source []byte) []byte {
		return blackfriday.Run(source)
	})
}

//////////////////////////////////////////////////////////////////////////////
//
//
//
// Build function
//
//
//
//////////////////////////////////////////////////////////////////////////////

func build(c *modulr.Context) error {
	//
	// Phase 0: Setup
	//
	// (No jobs should be enqueued here.)
	//

	c.Log.Debugf("Running build loop")

	err := envdecode.Decode(&conf)
	if err != nil {
		return err
	}

	// This is where we stored "versioned" assets like compiled JS and CSS.
	// These assets have a release number that we can increment and by
	// extension quickly invalidate.
	versionedAssetsDir := path.Join(c.TargetDir, "assets", Release)

	//
	// Phase 1
	//
	// The build is broken into phases because some jobs depend on jobs that
	// ran before them. For example, we need to parse all our article metadata
	// before we can create an article index and render the home page (which
	// contains a short list of articles).
	//
	// After each phase, we call `Wait` on our context which will wait for the
	// worker pool to finish all its current work and restart it to accept new
	// jobs after it has.
	//
	// The general rule is to make sure that work is done as early as it
	// possibly can be. e.g. Jobs with no dependencies should always run in
	// phase 1. Try to make sure that as few phases as necessary
	//

	//
	// Common directories
	//
	// Create these outside of the job system because jobs below may depend on
	// their existence.
	//

	commonDirs := []string{
		c.TargetDir + "/articles",
		c.TargetDir + "/fragments",
		c.TargetDir + "/passages",
		c.TargetDir + "/photos",
		TempDir,
		versionedAssetsDir,
	}
	for _, dir := range commonDirs {
		err = mfile.EnsureDir(c, dir)
		if err != nil {
			return nil
		}
	}

	//
	// Symlinks
	//

	commonSymlinks := [][2]string{
		{c.SourceDir + "/content/fonts", c.TargetDir + "/fonts"},
		{c.SourceDir + "/content/images", c.TargetDir + "/images"},
		{c.SourceDir + "/content/photographs", c.TargetDir + "/photographs"},
	}
	for _, link := range commonSymlinks {
		err := mfile.EnsureSymlink(c, link[0], link[1])
		if err != nil {
			return nil
		}
	}

	//
	// Articles
	//

	var articles []*Article
	articlesChanged := true

	{
		sources, err := mfile.ReadDir(c, c.SourceDir+"/content/articles")
		if err != nil {
			return err
		}

		if conf.Drafts {
			drafts, err := mfile.ReadDir(c, c.SourceDir+"/content/drafts")
			if err != nil {
				return err
			}
			sources = append(sources, drafts...)
		}

		for _, s := range sources {
			source := s

			c.Jobs <- func() (bool, error) {
				article, executed, err := renderArticle(c, source)
				if err != nil {
					return executed, err
				}

				articles = append(articles, article)
				return executed, nil
			}
		}
	}

	//
	// Fragments
	//

	var fragments []*Fragment
	fragmentsChanged := true

	{
		sources, err := mfile.ReadDir(c, c.SourceDir+"/content/fragments")
		if err != nil {
			return err
		}

		if conf.Drafts {
			drafts, err := mfile.ReadDir(c, c.SourceDir+"/content/fragments-drafts")
			if err != nil {
				return err
			}
			sources = append(sources, drafts...)
		}

		for _, s := range sources {
			source := s

			c.Jobs <- func() (bool, error) {
				fragment, executed, err := renderFragment(c, source)
				if err != nil {
					return executed, err
				}

				fragments = append(fragments, fragment)
				return executed, nil
			}
		}
	}

	//
	// Javascripts
	//

	{
		c.Jobs <- func() (bool, error) {
			return compileJavascripts(c, versionedAssetsDir)
		}
	}

	//
	// Pages
	//

	{
		// Note that we must always force loading of context for `_meta.yaml`
		// so that it's available if any individual page needs it.
		var meta map[string]*Page
		pagesMetaChanged, err := myaml.ParseFile(
			c.ForcedContext(), c.SourceDir+"/pages/_meta.yaml", &meta)
		if err != nil {
			return err
		}

		// If the master metadata file changed, then any page could potentially
		// have changed, so we'll have to re-render all of them: pass a forced
		// context into each page job.
		pageContext := c
		if pagesMetaChanged {
			pageContext = c.ForcedContext()
		}

		sources, err := mfile.ReadDir(c, c.SourceDir+"/pages")
		if err != nil {
			return err
		}

		for _, s := range sources {
			source := s

			c.Jobs <- func() (bool, error) {
				return renderPage(pageContext, meta, source)
			}
		}
	}

	//
	// Passages
	//

	var passages []*Passage
	passagesChanged := true

	{
		sources, err := mfile.ReadDir(c, c.SourceDir+"/content/passages")
		if err != nil {
			return err
		}

		if conf.Drafts {
			drafts, err := mfile.ReadDir(c, c.SourceDir+"/content/passages-drafts")
			if err != nil {
				return err
			}
			sources = append(sources, drafts...)
		}

		for _, s := range sources {
			source := s

			c.Jobs <- func() (bool, error) {
				passage, executed, err := renderPassage(c, source)
				if err != nil {
					return executed, err
				}

				passages = append(passages, passage)
				return executed, nil
			}
		}
	}

	//
	// Photos (read `_meta.yaml`)
	//

	var photos []*Photo
	var photosChanged bool

	{
		c.Jobs <- func() (bool, error) {
			var err error
			var photosWrapper PhotoWrapper

			// Always force this job so that we can get an accurate job count
			// when it comes to resizing photos below.
			photosChanged, err = myaml.ParseFile(
				c.ForcedContext(), c.SourceDir+"/content/photographs/_meta.yaml", &photosWrapper)
			if err != nil {
				return true, err
			}

			photos = photosWrapper.Photos
			return true, nil
		}
	}

	//
	// Robots.txt
	//

	{
		c.Jobs <- func() (bool, error) {
			return renderRobotsTxt(c, c.TargetDir+"/robots.txt")
		}
	}

	//
	// Sequences (read `_meta.yaml`)
	//

	sequences := make(map[string][]*Photo)
	sequencesChanged := make(map[string]bool)

	{
		sources, err := mfile.ReadDir(c, c.SourceDir+"/content/sequences")
		if err != nil {
			return err
		}

		if conf.Drafts {
			drafts, err := mfile.ReadDir(c, c.SourceDir+"/content/sequences-drafts")
			if err != nil {
				return err
			}
			sources = append(sources, drafts...)
		}

		for _, s := range sources {
			source := s

			c.Jobs <- func() (bool, error) {
				var err error
				var photosWrapper PhotoWrapper

				slug := path.Base(source)

				// Always force this job so that we can get an accurate job count
				// when it comes to resizing photos below.
				sequencesChanged[slug], err = myaml.ParseFile(
					c.ForcedContext(), source+"/_meta.yaml", &photosWrapper)
				if err != nil {
					return true, err
				}

				sequences[slug] = photosWrapper.Photos
				return true, nil
			}
		}
	}

	//
	// Stylesheets
	//

	{
		c.Jobs <- func() (bool, error) {
			return compileStylesheets(c, versionedAssetsDir)
		}
	}

	//
	// Talks
	//

	var talks []*t.Talk

	{
		sources, err := mfile.ReadDir(c, c.SourceDir+"/content/talks")
		if err != nil {
			return err
		}

		if conf.Drafts {
			drafts, err := mfile.ReadDir(c, c.SourceDir+"/content/talks-drafts")
			if err != nil {
				return err
			}
			sources = append(sources, drafts...)
		}

		for _, s := range sources {
			source := s

			c.Jobs <- func() (bool, error) {
				talk, executed, err := renderTalk(c, source)
				if !executed || err != nil {
					return executed, err
				}

				talks = append(talks, talk)
				return true, nil
			}
		}
	}

	//
	// Phase 2
	//

	if !c.Wait() {
		return nil
	}

	// Various sorts for anything that might need it.
	{
		sortArticles(articles)
		sortFragments(fragments)
		sortPassages(passages)
		sortPhotos(photos)
		sortTalks(talks)
	}

	//
	// Articles
	//

	// Index
	{
		c.Jobs <- func() (bool, error) {
			if !articlesChanged {
				return false, nil
			}

			return renderArticlesIndex(c, articles)
		}
	}

	// Feed (all)
	{
		c.Jobs <- func() (bool, error) {
			if !articlesChanged {
				return false, nil
			}

			return renderArticlesFeed(c, articles, nil)
		}
	}

	// Feed (Postgres)
	{
		c.Jobs <- func() (bool, error) {
			if !articlesChanged {
				return false, nil
			}

			return renderArticlesFeed(c, articles, tagPointer(tagPostgres))
		}
	}

	//
	// Fragments
	//

	// Index
	{
		c.Jobs <- func() (bool, error) {
			if !fragmentsChanged {
				return false, nil
			}

			return renderFragmentsIndex(c, fragments)
		}
	}

	// Feed
	{
		c.Jobs <- func() (bool, error) {
			if !fragmentsChanged {
				return false, nil
			}

			return renderFragmentsFeed(c, fragments)
		}
	}

	//
	// Home
	//

	{
		c.Jobs <- func() (bool, error) {
			if !articlesChanged && !fragmentsChanged && !photosChanged {
				return false, nil
			}

			return renderHome(c, articles, fragments, photos)
		}
	}

	//
	// Passages
	//

	{
		c.Jobs <- func() (bool, error) {
			if !passagesChanged {
				return false, nil
			}

			return renderPassagesIndex(c, passages)
		}
	}

	//
	// Photos (index / fetch + resize)
	//

	// Photo index
	{
		c.Jobs <- func() (bool, error) {
			if !photosChanged {
				return false, nil
			}

			return renderPhotoIndex(c, photos)
		}
	}

	// Photo fetch + resize
	{
		for _, p := range photos {
			photo := p

			c.Jobs <- func() (bool, error) {
				return fetchAndResizePhoto(c, c.SourceDir+"/content/photographs", photo)
			}
		}
	}

	//
	// Sequences (index / fetch + resize)
	//

	{
		for s, p := range sequences {
			slug := s
			photos := p

			var err error
			err = mfile.EnsureDir(c, c.TargetDir+"/sequences/"+slug)
			if err != nil {
				return err
			}
			err = mfile.EnsureDir(c, c.SourceDir+"/content/photographs/sequences/"+slug)
			if err != nil {
				return err
			}

			for _, p := range photos {
				photo := p

				// Sequence page
				c.Jobs <- func() (bool, error) {
					if !sequencesChanged[slug] {
						return false, nil
					}

					return renderSequence(c, slug, photo)
				}

				// Sequence fetch + resize
				c.Jobs <- func() (bool, error) {
					return fetchAndResizePhoto(c, c.SourceDir+"/content/photographs/sequences/"+slug, photo)
				}
			}
		}
	}

	return nil
}

//////////////////////////////////////////////////////////////////////////////
//
//
//
// Structs
//
//
//
//////////////////////////////////////////////////////////////////////////////

// Article represents an article to be rendered.
type Article struct {
	// Attributions are any attributions for content that may be included in
	// the article (like an image in the header for example).
	Attributions string `yaml:"attributions"`

	// Content is the HTML content of the article. It isn't included as YAML
	// frontmatter, and is rather split out of an article's Markdown file,
	// rendered, and then added separately.
	Content string `yaml:"-"`

	// Draft indicates that the article is not yet published.
	Draft bool `yaml:"-"`

	// HNLink is an optional link to comments on Hacker News.
	HNLink string `yaml:"hn_link"`

	// Hook is a leading sentence or two to succinctly introduce the article.
	Hook string `yaml:"hook"`

	// HookImageURL is the URL for a hook image for the article (to be shown on
	// the article index) if one was found.
	HookImageURL string `yaml:"-"`

	// Image is an optional image that may be included with an article.
	Image string `yaml:"image"`

	// Location is the geographical location where this article was written.
	Location string `yaml:"location"`

	// PublishedAt is when the article was published.
	PublishedAt *time.Time `yaml:"published_at"`

	// Slug is a unique identifier for the article that also helps determine
	// where it's addressable by URL.
	Slug string `yaml:"-"`

	// Tags are the set of tags that the article is tagged with.
	Tags []Tag `yaml:"tags"`

	// Title is the article's title.
	Title string `yaml:"title"`

	// TOC is the HTML rendered table of contents of the article. It isn't
	// included as YAML frontmatter, but rather calculated from the article's
	// content, rendered, and then added separately.
	TOC string `yaml:"-"`
}

// publishingInfo produces a brief spiel about publication which is intended to
// go into the left sidebar when an article is shown.
func (a *Article) publishingInfo() string {
	return `<p><strong>Article</strong><br>` + a.Title + `</p>` +
		`<p><strong>Published</strong><br>` + a.PublishedAt.Format("January 2, 2006") + `</p> ` +
		`<p><strong>Location</strong><br>` + a.Location + `</p>` +
		twitterInfo
}

// taggedWith returns true if the given tag is in this article's set of tags
// and false otherwise.
func (a *Article) taggedWith(tag Tag) bool {
	for _, t := range a.Tags {
		if t == tag {
			return true
		}
	}

	return false
}

func (a *Article) validate(source string) error {
	if a.Location == "" {
		return fmt.Errorf("No location for article: %v", source)
	}

	if a.Title == "" {
		return fmt.Errorf("No title for article: %v", source)
	}

	if a.PublishedAt == nil {
		return fmt.Errorf("No publish date for article: %v", source)
	}

	return nil
}

// Conf contains configuration information for the command. It's extracted from
// environment variables.
type Conf struct {
	// AtomAuthorName is the name of the author to include in Atom feeds.
	AtomAuthorName string `env:"AUTHOR_NAME,default=Brandur Leach"`

	// AtomAuthorName is the URL of the author to include in Atom feeds.
	AtomAuthorURL string `env:"AUTHOR_URL,default=https://brandur.org"`

	// BlackSwanDatabaseURL is a connection string for a database to connect to
	// in order to extract books, tweets, runs, etc.
	BlackSwanDatabaseURL string `env:"BLACK_SWAN_DATABASE_URL"`

	// Concurrency is the number of build Goroutines that will be used to
	// perform build work items.
	Concurrency int `env:"CONCURRENCY,default=30"`

	// Drafts is whether drafts of articles and fragments should be compiled
	// along with their published versions.
	//
	// Activating drafts also prompts the creation of a robots.txt to make sure
	// that drafts aren't inadvertently accessed by web crawlers.
	Drafts bool `env:"DRAFTS,default=false"`

	// ContentOnly tells the build step that it should build using only files
	// in the content directory. This means that information imported from a
	// Black Swan database (reading, tweets, etc.) will be skipped. This is
	// a speed optimization for use while watching for file changes.
	ContentOnly bool `env:"CONTENT_ONLY,default=false"`

	// GoogleAnalyticsID is the account identifier for Google Analytics to use.
	GoogleAnalyticsID string `env:"GOOGLE_ANALYTICS_ID"`

	// LocalFonts starts using locally downloaded versions of Google Fonts.
	// This is not ideal for real deployment because you won't be able to
	// leverage Google's CDN and the caching that goes with it, and may not get
	// the font format for requesting browsers, but good for airplane rides
	// where you otherwise wouldn't have the fonts.
	LocalFonts bool `env:"LOCAL_FONTS,default=false"`

	// NumAtomEntries is the number of entries to put in Atom feeds.
	NumAtomEntries int `env:"NUM_ATOM_ENTRIES,default=20"`

	// SiteURL is the absolute URL where the compiled site will be hosted.
	SiteURL string `env:"SITE_URL,default=https://brandur.org"`

	// TargetDir is the target location where the site will be built to.
	TargetDir string `env:"TARGET_DIR,default=./public"`

	// Verbose is whether the program will print debug output as it's running.
	Verbose bool `env:"VERBOSE,default=false"`
}

// Fragment represents a fragment (that is, a short "stream of consciousness"
// style article) to be rendered.
type Fragment struct {
	// Attributions are any attributions for content that may be included in
	// the article (like an image in the header for example).
	Attributions string `yaml:"attributions"`

	// Content is the HTML content of the fragment. It isn't included as YAML
	// frontmatter, and is rather split out of an fragment's Markdown file,
	// rendered, and then added separately.
	Content string `yaml:"-"`

	// Draft indicates that the fragment is not yet published.
	Draft bool `yaml:"-"`

	// HNLink is an optional link to comments on Hacker News.
	HNLink string `yaml:"hn_link"`

	// Hook is a leading sentence or two to succinctly introduce the fragment.
	Hook string `yaml:"hook"`

	// Image is an optional image that may be included with a fragment.
	Image string `yaml:"image"`

	// Location is the geographical location where this article was written.
	Location string `yaml:"location"`

	// PublishedAt is when the fragment was published.
	PublishedAt *time.Time `yaml:"published_at"`

	// Slug is a unique identifier for the fragment that also helps determine
	// where it's addressable by URL.
	Slug string `yaml:"-"`

	// Title is the fragment's title.
	Title string `yaml:"title"`
}

// PublishingInfo produces a brief spiel about publication which is intended to
// go into the left sidebar when a fragment is shown.
func (f *Fragment) publishingInfo() string {
	s := `<p><strong>Fragment</strong><br>` + f.Title + `</p>` +
		`<p><strong>Published</strong><br>` + f.PublishedAt.Format("January 2, 2006") + `</p> `

	if f.Location != "" {
		s += `<p><strong>Location</strong><br>` + f.Location + `</p>`
	}

	s += twitterInfo
	return s
}

func (f *Fragment) validate(source string) error {
	if f.Title == "" {
		return fmt.Errorf("No title for fragment: %v", source)
	}

	if f.PublishedAt == nil {
		return fmt.Errorf("No publish date for fragment: %v", source)
	}

	return nil
}

// Page is the metadata for a static HTML page generated from an ACE file.
// Currently the layouting system of ACE doesn't allow us to pass metadata up
// very well, so we have this instead.
type Page struct {
	// BodyClass is the CSS class that will be assigned to the body tag when
	// the page is rendered.
	BodyClass string `yaml:"body_class"`

	// Title is the HTML title that will be assigned to the page when it's
	// rendered.
	Title string `yaml:"title"`
}

// Passage represents a single burst of the Passage & Glass newsletter to be
// rendered.
type Passage struct {
	// Content is the HTML content of the passage. It isn't included as YAML
	// frontmatter, and is rather split out of an passage's Markdown file,
	// rendered, and then added separately.
	Content string `yaml:"-"`

	// ContentRaw is the raw Markdown content of the passage.
	ContentRaw string `yaml:"-"`

	// Draft indicates that the passage is not yet published.
	Draft bool `yaml:"-"`

	// Issue is the issue number of the passage like "001". Notably, it's a
	// number, but zero-padded.
	Issue string `yaml:"-"`

	// PublishedAt is when the passage was published.
	PublishedAt *time.Time `yaml:"published_at"`

	// Slug is a unique identifier for the passage that also helps determine
	// where it's addressable by URL. It's a combination of an issue number
	// (like `001` and a short identifier).
	Slug string `yaml:"-"`

	// Title is the passage's title.
	Title string `yaml:"title"`
}

func (p *Passage) validate(source string) error {
	if p.Title == "" {
		return fmt.Errorf("No title for passage: %v", source)
	}

	if p.PublishedAt == nil {
		return fmt.Errorf("No publish date for passage: %v", source)
	}

	return nil
}

// Photo is a photograph.
type Photo struct {
	// Description is the description of the photograph.
	Description string `yaml:"description"`

	// KeepInHomeRotation is a special override for photos I really like that
	// keeps them in the home page's random rotation. The rotation then
	// consists of either a recent photo or one of these explicitly selected
	// old ones.
	KeepInHomeRotation bool `yaml:"keep_in_home_rotation"`

	// OriginalImageURL is the location where the original-sized version of the
	// photo can be downloaded from.
	OriginalImageURL string `yaml:"original_image_url"`

	// OccurredAt is UTC time when the photo was published.
	OccurredAt *time.Time `yaml:"occurred_at"`

	// Slug is a unique identifier for the photo. Originally these were
	// generated from Flickr, but I've since just started reusing them for
	// filenames.
	Slug string `yaml:"slug"`

	// Title is the title of the photograph.
	Title string `yaml:"title"`
}

// PhotoWrapper is a data structure intended to represent the data structure at
// the top level of photograph data file `content/photographs/_meta.yaml`.
type PhotoWrapper struct {
	// Photos is a collection of photos within the top-level wrapper.
	Photos []*Photo `yaml:"photographs"`
}

// Tag is a symbol assigned to an article to categorize it.
//
// This feature is not meanted to be overused. It's really just for tagging
// a few particular things so that we can generate content-specific feeds for
// certain aggregates (so far just Planet Postgres).
type Tag string

// articleYear holds a collection of articles grouped by year.
type articleYear struct {
	Year     int
	Articles []*Article
}

// fragmentYear holds a collection of fragments grouped by year.
type fragmentYear struct {
	Year      int
	Fragments []*Fragment
}

// twitterCard represents a Twitter "card" (i.e. one of those rich media boxes
// that sometimes appear under tweets official clients) for use in templates.
type twitterCard struct {
	// Description is the title to show in the card.
	Title string

	// Description is the description to show in the card.
	Description string

	// ImageURL is the URL to the image to show in the card. It should be
	// absolute because Twitter will need to be able to fetch it from our
	// servers. Leave blank if there is no image.
	ImageURL string
}

//////////////////////////////////////////////////////////////////////////////
//
//
//
// Private
//
//
//
//////////////////////////////////////////////////////////////////////////////

func aceOptions() *ace.Options {
	return &ace.Options{FuncMap: templatehelpers.FuncMap}
}

func compileJavascripts(c *modulr.Context, versionedAssetsDir string) (bool, error) {
	sourceDir := c.SourceDir + "/content/javascripts"

	sources, err := mfile.ReadDir(c, sourceDir)
	if err != nil {
		return false, err
	}

	changed := c.ChangedAny(sources)
	if !changed && !c.Forced() {
		return false, nil
	}

	err = assets.CompileJavascripts(
		sourceDir,
		versionedAssetsDir+"/app.js")
	return true, err
}

func compileStylesheets(c *modulr.Context, versionedAssetsDir string) (bool, error) {
	sourceDir := c.SourceDir + "/content/stylesheets"

	sources, err := mfile.ReadDir(c, sourceDir)
	if err != nil {
		return false, err
	}

	changed := c.ChangedAny(sources)
	if !changed && !c.Forced() {
		return false, nil
	}

	err = assets.CompileStylesheets(
		sourceDir,
		versionedAssetsDir+"/app.css")
	return true, err
}

func fetchAndResizePhoto(c *modulr.Context, dir string, photo *Photo) (bool, error) {
	// source without an extension, e.g. `content/photographs/123`
	sourceNoExt := filepath.Join(dir, photo.Slug)

	// A "marker" is an empty file that we commit to a photograph directory
	// that indicates that we've already done the work to fetch and resize a
	// photo. It allows us to skip duplicate work even if we don't have the
	// work's results available locally. This is important for CI where we
	// store results to an S3 bucket, but don't pull them all back down again
	// for every build.
	markerPath := sourceNoExt + ".marker"

	if mfile.Exists(markerPath) {
		c.Log.Debugf("Skipping photo fetch + resize because marker exists: %s",
			markerPath)
		return false, nil
	}

	originalPath := filepath.Join(TempDir, photo.Slug+"_original.jpg")

	err := fetchURL(c, photo.OriginalImageURL, originalPath)
	if err != nil {
		return true, errors.Wrapf(err, "Error fetching photograph: %s", photo.Slug)
	}

	resizeMatrix := []struct {
		Target string
		Width  int
	}{
		{sourceNoExt + ".jpg", 333},
		{sourceNoExt + "@2x.jpg", 667},
		{sourceNoExt + "_large.jpg", 1500},
		{sourceNoExt + "_large@2x.jpg", 3000},
	}
	for _, resize := range resizeMatrix {
		err := resizeImage(c, originalPath, resize.Target, resize.Width)
		if err != nil {
			return true, errors.Wrapf(err, "Error resizing photograph: %s", photo.Slug)
		}
	}

	// After everything is done, created a marker file to indicate that the
	// work doesn't need to be redone.
	file, err := os.OpenFile(markerPath, os.O_RDONLY|os.O_CREATE, 0755)
	if err != nil {
		return true, errors.Wrapf(err, "Error creating marker for photograph: ", photo.Slug)
	}
	file.Close()

	return true, nil
}

// fetchURL is a helper for fetching a file via HTTP and storing it the local
// filesystem.
func fetchURL(c *modulr.Context, source, target string) error {
	c.Log.Debugf("Fetching file: %v", source)

	resp, err := http.Get(source)
	if err != nil {
		return errors.Wrapf(err, "Error fetching: %v", source)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("Unexpected status code fetching '%v': %d",
			source, resp.StatusCode)
	}

	f, err := os.Create(target)
	if err != nil {
		return errors.Wrapf(err, "Error creating: %v", target)
	}
	defer f.Close()

	w := bufio.NewWriter(f)

	// probably not needed
	defer w.Flush()

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		return errors.Wrapf(err, "Error copying to '%v' from HTTP response",
			target)
	}

	return nil
}

// Gets a map of local values for use while rendering a template and includes
// a few "special" values that are globally relevant to all templates.
func getLocals(title string, locals map[string]interface{}) map[string]interface{} {
	defaults := map[string]interface{}{
		"BodyClass":         "",
		"GoogleAnalyticsID": conf.GoogleAnalyticsID,
		"LocalFonts":        conf.LocalFonts,
		"Release":           Release,
		"Title":             title,
		"TwitterCard":       nil,
		"ViewportWidth":     "device-width",
	}

	for k, v := range locals {
		defaults[k] = v
	}

	return defaults
}

func groupArticlesByYear(articles []*Article) []*articleYear {
	var year *articleYear
	var years []*articleYear

	for _, article := range articles {
		if year == nil || year.Year != article.PublishedAt.Year() {
			year = &articleYear{article.PublishedAt.Year(), nil}
			years = append(years, year)
		}

		year.Articles = append(year.Articles, article)
	}

	return years
}

func groupFragmentsByYear(fragments []*Fragment) []*fragmentYear {
	var year *fragmentYear
	var years []*fragmentYear

	for _, fragment := range fragments {
		if year == nil || year.Year != fragment.PublishedAt.Year() {
			year = &fragmentYear{fragment.PublishedAt.Year(), nil}
			years = append(years, year)
		}

		year.Fragments = append(year.Fragments, fragment)
	}

	return years
}

// Checks if the path exists as a common image format (.jpg or .png only). If
// so, returns the discovered extension (e.g. "jpg") and boolean true.
// Otherwise returns an empty string and boolean false.
func pathAsImage(extensionlessPath string) (string, bool) {
	// extensions must be lowercased
	formats := []string{"jpg", "png"}

	for _, format := range formats {
		_, err := os.Stat(extensionlessPath + "." + format)
		if err != nil {
			continue
		}

		return format, true
	}

	return "", false
}

func renderArticle(c *modulr.Context, source string) (*Article, bool, error) {
	// We can't really tell whether we need to rebuild our articles index, so
	// we always at least parse every article to get its metadata struct, and
	// then rebuild the index every time. If the source was unchanged though,
	// we stop after getting its metadata.
	forceC := c.ForcedContext()

	var article Article
	data, changed, err := myaml.ParseFileFrontmatter(forceC, source, &article)
	if err != nil {
		return nil, true, err
	}

	err = article.validate(source)
	if err != nil {
		return nil, true, err
	}

	article.Draft = strings.Contains(filepath.Base(filepath.Dir(source)), "drafts")
	article.Slug = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))

	// See comment above: we always parse metadata, but if the file was
	// unchanged (determined from the `executed` result), it's okay not to
	// re-render it.
	if !changed && !c.Forced() {
		return &article, true, nil
	}

	article.Content = renderComplexMarkdown(string(data), nil)

	article.TOC, err = mtoc.RenderFromHTML(article.Content)
	if err != nil {
		return nil, true, err
	}

	format, ok := pathAsImage(
		path.Join(c.SourceDir, "content", "images", article.Slug, "hook"),
	)
	if ok {
		article.HookImageURL = "/assets/" + article.Slug + "/hook." + format
	}

	card := &twitterCard{
		Title:       article.Title,
		Description: article.Hook,
	}
	format, ok = pathAsImage(
		path.Join(c.SourceDir, "content", "images", article.Slug, "twitter@2x"),
	)
	if ok {
		card.ImageURL = AbsoluteURL + "/assets/" + article.Slug + "/twitter@2x." + format
	}

	locals := getLocals(article.Title, map[string]interface{}{
		"Article":        article,
		"PublishingInfo": article.publishingInfo(),
		"TwitterCard":    card,
	})

	// Always use force context because if we made it to here we know that our
	// sources have changed.
	_, err = mace.Render(c.ForcedContext(), MainLayout, ViewsDir+"/articles/show",
		path.Join(c.TargetDir, article.Slug), aceOptions(), locals)
	if err != nil {
		return nil, true, err
	}

	return &article, true, nil
}

func renderArticlesIndex(c *modulr.Context, articles []*Article) (bool, error) {
	articlesByYear := groupArticlesByYear(articles)

	locals := getLocals("Articles", map[string]interface{}{
		"ArticlesByYear": articlesByYear,
	})

	_, err := mace.Render(c.ForcedContext(), MainLayout, ViewsDir+"/articles/index",
		c.TargetDir+"/articles/index.html", aceOptions(), locals)
	if err != nil {
		return true, err
	}

	return true, nil
}

func renderArticlesFeed(c *modulr.Context, articles []*Article, tag *Tag) (bool, error) {
	name := "articles"
	if tag != nil {
		name = fmt.Sprintf("articles-%s", *tag)
	}
	filename := name + ".atom"

	title := "Articles - brandur.org"
	if tag != nil {
		title = fmt.Sprintf("Articles (%s) - brandur.org", *tag)
	}

	feed := &atom.Feed{
		Title: title,
		ID:    "tag:brandur.org.org,2013:/" + name,

		Links: []*atom.Link{
			{Rel: "self", Type: "application/atom+xml", Href: "https://brandur.org/" + filename},
			{Rel: "alternate", Type: "text/html", Href: "https://brandur.org"},
		},
	}

	if len(articles) > 0 {
		feed.Updated = *articles[0].PublishedAt
	}

	for i, article := range articles {
		if tag != nil && !article.taggedWith(*tag) {
			continue
		}

		if i >= conf.NumAtomEntries {
			break
		}

		entry := &atom.Entry{
			Title:     article.Title,
			Content:   &atom.EntryContent{Content: article.Content, Type: "html"},
			Published: *article.PublishedAt,
			Updated:   *article.PublishedAt,
			Link:      &atom.Link{Href: conf.SiteURL + "/" + article.Slug},
			ID:        "tag:brandur.org," + article.PublishedAt.Format("2006-01-02") + ":" + article.Slug,

			AuthorName: conf.AtomAuthorName,
			AuthorURI:  conf.AtomAuthorURL,
		}
		feed.Entries = append(feed.Entries, entry)
	}

	f, err := os.Create(path.Join(conf.TargetDir, filename))
	if err != nil {
		return true, err
	}
	defer f.Close()

	return true, feed.Encode(f, "  ")
}

func renderFragment(c *modulr.Context, source string) (*Fragment, bool, error) {
	// We can't really tell whether we need to rebuild our fragments index, so
	// we always at least parse every fragment to get its metadata struct, and
	// then rebuild the index every time. If the source was unchanged though,
	// we stop after getting its metadata.
	forceC := c.ForcedContext()

	var fragment Fragment
	data, changed, err := myaml.ParseFileFrontmatter(forceC, source, &fragment)
	if err != nil {
		return nil, true, err
	}

	err = fragment.validate(source)
	if err != nil {
		return nil, true, err
	}

	fragment.Draft = strings.Contains(filepath.Base(filepath.Dir(source)), "drafts")
	fragment.Slug = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))

	// See comment above: we always parse metadata, but if the file was
	// unchanged (determined from the `executed` result), it's okay not to
	// re-render it.
	if !changed && !c.Forced() {
		return &fragment, true, nil
	}

	fragment.Content = renderComplexMarkdown(string(data), nil)

	card := &twitterCard{
		Title:       fragment.Title,
		Description: fragment.Hook,
	}
	format, ok := pathAsImage(
		path.Join(c.SourceDir, "content", "images", "fragments", fragment.Slug, "twitter@2x"),
	)
	if ok {
		card.ImageURL = AbsoluteURL + "/assets/fragments/" + fragment.Slug + "/twitter@2x." + format
	}

	locals := getLocals(fragment.Title, map[string]interface{}{
		"Fragment":       fragment,
		"PublishingInfo": fragment.publishingInfo(),
		"TwitterCard":    card,
	})

	// Always use force context because if we made it to here we know that our
	// sources have changed.
	_, err = mace.Render(c.ForcedContext(), MainLayout, ViewsDir+"/fragments/show",
		path.Join(c.TargetDir, "fragments", fragment.Slug), aceOptions(), locals)
	if err != nil {
		return nil, true, err
	}

	return &fragment, true, nil
}

func renderFragmentsFeed(c *modulr.Context, fragments []*Fragment) (bool, error) {
	feed := &atom.Feed{
		Title: "Fragments - brandur.org",
		ID:    "tag:brandur.org.org,2013:/fragments",

		Links: []*atom.Link{
			{Rel: "self", Type: "application/atom+xml", Href: "https://brandur.org/fragments.atom"},
			{Rel: "alternate", Type: "text/html", Href: "https://brandur.org"},
		},
	}

	if len(fragments) > 0 {
		feed.Updated = *fragments[0].PublishedAt
	}

	for i, fragment := range fragments {
		if i >= conf.NumAtomEntries {
			break
		}

		entry := &atom.Entry{
			Title:     fragment.Title,
			Content:   &atom.EntryContent{Content: fragment.Content, Type: "html"},
			Published: *fragment.PublishedAt,
			Updated:   *fragment.PublishedAt,
			Link:      &atom.Link{Href: conf.SiteURL + "/fragments/" + fragment.Slug},
			ID:        "tag:brandur.org," + fragment.PublishedAt.Format("2006-01-02") + ":fragments/" + fragment.Slug,

			AuthorName: conf.AtomAuthorName,
			AuthorURI:  conf.AtomAuthorURL,
		}
		feed.Entries = append(feed.Entries, entry)
	}

	f, err := os.Create(conf.TargetDir + "/fragments.atom")
	if err != nil {
		return true, err
	}
	defer f.Close()

	return true, feed.Encode(f, "  ")
}

func renderFragmentsIndex(c *modulr.Context, fragments []*Fragment) (bool, error) {
	fragmentsByYear := groupFragmentsByYear(fragments)

	locals := getLocals("Fragments", map[string]interface{}{
		"FragmentsByYear": fragmentsByYear,
	})

	_, err := mace.Render(c.ForcedContext(), MainLayout, ViewsDir+"/fragments/index",
		c.TargetDir+"/fragments/index.html", aceOptions(), locals)
	if err != nil {
		return true, err
	}

	return true, nil
}

func renderPassage(c *modulr.Context, source string) (*Passage, bool, error) {
	// We can't really tell whether we need to rebuild our passages index, so
	// we always at least parse every passage to get its metadata struct, and
	// then rebuild the index every time. If the source was unchanged though,
	// we stop after getting its metadata.
	forceC := c.ForcedContext()

	var passage Passage
	data, changed, err := myaml.ParseFileFrontmatter(forceC, source, &passage)
	if err != nil {
		return nil, true, err
	}

	err = passage.validate(source)
	if err != nil {
		return nil, true, err
	}

	passage.ContentRaw = string(data)
	passage.Draft = strings.Contains(filepath.Base(filepath.Dir(source)), "drafts")
	passage.Slug = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))

	slugParts := strings.Split(passage.Slug, "-")
	if len(slugParts) < 2 {
		return nil, true, fmt.Errorf("Expected passage slug to contain issue number: %v",
			passage.Slug)
	}
	passage.Issue = slugParts[0]

	// See comment above: we always parse metadata, but if the file was
	// unchanged (determined from the `executed` result), it's okay not to
	// re-render it.
	if !changed && !c.Forced() {
		return &passage, true, nil
	}

	email := false

	passage.Content = renderComplexMarkdown(passage.ContentRaw, &markdown.RenderOptions{
		AbsoluteURLs:    email,
		NoFootnoteLinks: email,
		NoHeaderLinks:   email,
		NoRetina:        true,
	})

	locals := getLocals(passage.Title, map[string]interface{}{
		"InEmail": false,
		"Passage": passage,
	})

	_, err = mace.Render(c.ForcedContext(), PassageLayout, ViewsDir+"/passages/show",
		c.TargetDir+"/passages/"+passage.Slug, aceOptions(), locals)
	if err != nil {
		return nil, true, err
	}

	return &passage, true, nil
}

func renderPassagesIndex(c *modulr.Context, passages []*Passage) (bool, error) {
	locals := getLocals("Passages", map[string]interface{}{
		"Passages": passages,
	})

	_, err := mace.Render(c.ForcedContext(), PassageLayout, ViewsDir+"/passages/index",
		c.TargetDir+"/passages/index.html", aceOptions(), locals)
	if err != nil {
		return true, err
	}

	return true, nil
}

func renderHome(c *modulr.Context, articles []*Article, fragments []*Fragment, photos []*Photo) (bool, error) {
	if len(articles) > 3 {
		articles = articles[0:3]
	}

	// Try just one fragment for now to better balance the page's height.
	if len(fragments) > 1 {
		fragments = fragments[0:1]
	}

	// Find a random photo to put on the homepage.
	photo := selectRandomPhoto(photos)

	locals := getLocals("brandur.org", map[string]interface{}{
		"Articles":  articles,
		"BodyClass": "index",
		"Fragments": fragments,
		"Photo":     photo,
	})

	_, err := mace.Render(c.ForcedContext(), MainLayout, ViewsDir+"/index",
		c.TargetDir+"/index.html", aceOptions(), locals)
	if err != nil {
		return true, err
	}

	return true, nil
}

func renderPage(c *modulr.Context, pagesMeta map[string]*Page, source string) (bool, error) {
	// Strip the `.ace` extension. Ace adds its own when rendering, and we
	// don't want it on the output files.
	source = strings.TrimSuffix(source, path.Ext(source))

	// Remove the "./pages" directory, but keep the rest of the path.
	//
	// Looks something like "about".
	pagePath := strings.TrimPrefix(mfile.MustAbs(source),
		mfile.MustAbs("./pages")+"/")

	// Looks something like "./public/about".
	target := path.Join(c.TargetDir, pagePath)

	// Put a ".html" on if this page is an index. This will allow our local
	// server to serve it at a directory path, and our upload script is smart
	// enough to do the right thing with it as well.
	if path.Base(pagePath) == "index" {
		target += ".html"
	}

	locals := map[string]interface{}{
		"BodyClass": "",
		"Title":     "Untitled Page",
	}

	meta, ok := pagesMeta[pagePath]
	if ok {
		locals = map[string]interface{}{
			"BodyClass": meta.BodyClass,
			"Title":     meta.Title,
		}
	} else {
		c.Log.Errorf("No page meta information: %v", pagePath)
	}

	locals = getLocals("Page", locals)

	err := mfile.EnsureDir(c, path.Dir(target))
	if err != nil {
		return true, err
	}

	changed, err := mace.Render(c, MainLayout, source, target, aceOptions(), locals)
	executed := changed || c.Forced()
	if err != nil {
		return executed, err
	}

	return executed, nil
}

func renderPhotoIndex(c *modulr.Context, photos []*Photo) (bool, error) {
	locals := getLocals("Photos", map[string]interface{}{
		"BodyClass":     "photos",
		"Photos":        photos,
		"ViewportWidth": 600,
	})

	// If we called in here then `photos` has changed, so make sure to force a
	// render.
	_, err := mace.Render(c.ForcedContext(), MainLayout, ViewsDir+"/photos/index",
		c.TargetDir+"/photos/index.html", aceOptions(), locals)
	return true, err
}

func renderRobotsTxt(c *modulr.Context, target string) (bool, error) {
	if !c.FirstRun && !c.Forced() {
		return false, nil
	}

	var content string
	if conf.Drafts {
		// Allow Twitterbot so that we can preview card images on dev.
		content = `User-agent: Twitterbot
Disallow:

User-agent: *
Disallow: /
`
	} else {
		// Disallow acccess to photos because the content isn't very
		// interesting for robots and they're bandwidth heavy.
		content = `User-agent: *
Disallow: /photographs/
Disallow: /photos
`
	}

	outFile, err := os.Create(target)
	if err != nil {
		return true, err
	}
	outFile.WriteString(content)
	outFile.Close()

	return true, nil
}

func renderSequence(c *modulr.Context, sequenceName string, photo *Photo) (bool, error) {
	title := fmt.Sprintf("%s — %s", photo.Title, sequenceName)
	description := string(mmarkdown.Render(c, []byte(photo.Description)))

	locals := getLocals(title, map[string]interface{}{
		"BodyClass":     "sequences-photo",
		"Description":   description,
		"Photo":         photo,
		"SequenceName":  sequenceName,
		"ViewportWidth": 600,
	})

	_, err := mace.Render(c.ForcedContext(), MainLayout, ViewsDir+"/sequences/photo",
		path.Join(c.TargetDir, "sequences", sequenceName, photo.Slug), aceOptions(), locals)
	return true, err
}

func renderTalk(c *modulr.Context, source string) (*t.Talk, bool, error) {
	changed := c.Changed(source)
	if !changed && !c.Forced() {
		return nil, false, nil
	}

	// TODO: modulr-ize this package
	talk, err := t.Render(
		c.SourceDir+"/content", filepath.Dir(source), filepath.Base(source))
	if err != nil {
		return nil, true, err
	}

	locals := getLocals(talk.Title, map[string]interface{}{
		"BodyClass":      "talk",
		"PublishingInfo": talk.PublishingInfo(),
		"Talk":           talk,
	})

	_, err = mace.Render(c, MainLayout, ViewsDir+"/talks/show",
		path.Join(c.TargetDir, talk.Slug), aceOptions(), locals)
	if err != nil {
		return talk, true, err
	}

	return talk, true, nil
}

func resizeImage(c *modulr.Context, source, target string, width int) error {
	cmd := exec.Command(
		"gm",
		"convert",
		source,
		"-auto-orient",
		"-resize",
		fmt.Sprintf("%vx", width),
		"-quality",
		"85",
		target,
	)

	var errOut bytes.Buffer
	cmd.Stderr = &errOut

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("%v (stderr: %v)", err, errOut.String())
	}

	return nil
}

func selectRandomPhoto(photos []*Photo) *Photo {
	if len(photos) < 1 {
		return nil
	}

	numRecent := 20
	if len(photos) < numRecent {
		numRecent = len(photos)
	}

	// All recent photos go into the random selection.
	randomPhotos := photos[0:numRecent]

	// Older photos that are good enough that I've explicitly tagged them
	// as such also get considered for the rotation.
	if len(photos) > numRecent {
		olderPhotos := photos[numRecent : len(photos)-1]

		for _, photo := range olderPhotos {
			if photo.KeepInHomeRotation {
				randomPhotos = append(randomPhotos, photo)
			}
		}
	}

	return randomPhotos[rand.Intn(len(randomPhotos))]
}

func sortArticles(articles []*Article) {
	sort.Slice(articles, func(i, j int) bool {
		return articles[j].PublishedAt.Before(*articles[i].PublishedAt)
	})
}

func sortFragments(fragments []*Fragment) {
	sort.Slice(fragments, func(i, j int) bool {
		return fragments[j].PublishedAt.Before(*fragments[i].PublishedAt)
	})
}

func sortPassages(passages []*Passage) {
	sort.Slice(passages, func(i, j int) bool {
		return passages[j].PublishedAt.Before(*passages[i].PublishedAt)
	})
}

func sortPhotos(photos []*Photo) {
	sort.Slice(photos, func(i, j int) bool {
		return photos[j].OccurredAt.Before(*photos[i].OccurredAt)
	})
}

func sortTalks(talks []*t.Talk) {
	sort.Slice(talks, func(i, j int) bool {
		return talks[j].PublishedAt.Before(*talks[i].PublishedAt)
	})
}

// Gets a pointer to a tag just to work around the fact that you can take the
// address of a constant like `tagPostgres`.
func tagPointer(tag Tag) *Tag {
	return &tag
}
