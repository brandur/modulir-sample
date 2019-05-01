package main

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/brandur/modulr"
	"github.com/brandur/modulr/mod/mace"
	"github.com/brandur/modulr/mod/mfile"
	"github.com/brandur/modulr/mod/mmarkdown"
	"github.com/brandur/modulr/mod/mtoc"
	"github.com/brandur/modulr/mod/myaml"
	"github.com/joeshaw/envdecode"
	//"github.com/pkg/errors"
	//"gopkg.in/yaml.v2"
)

//
// Main
//

func main() {
	modulr.BuildLoop(nil, build)
}

//
// Constants
//

const (
	// LayoutsDir is the source directory for view layouts.
	LayoutsDir = "./layouts"

	// MainLayout is the site's main layout.
	MainLayout = LayoutsDir + "/main"

	// Release is the asset version of the site. Bump when any assets are
	// updated to blow away any browser caches.
	Release = "74"

	// ViewsDir is the source directory for views.
	ViewsDir = "./views"
)

// TwitterInfo is some HTML that includes a Twitter link which can be appended
// to the publishing info of various content.
const twitterInfo = `<p>Find me on Twitter at ` +
	`<strong><a href="https://twitter.com/brandur">@brandur</a></strong>.</p>`

//
// Variables
//

// Left as a global for now for the sake of convenience, but it's not used in
// very many places and can probably be refactored as a local if desired.
var conf Conf

//
// Build function
//

func build(c *modulr.Context) error {
	//
	// Phase 0: Setup
	//
	// (No jobs should be enqueued here.)

	c.Log.Debugf("Running build loop")

	err := envdecode.Decode(&conf)
	if err != nil {
		return err
	}

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

	c.Jobs <- func() error {
		return mfile.CopyFileToDir(c, c.SourceDir+"/hello.md", c.TargetDir)
	}

	//
	// Articles
	//

	var articles []*Article

	articleSources, err := mfile.ReadDir(c, c.SourceDir+"/content/articles")
	if err != nil {
		return err
	}

	if conf.Drafts {
		drafts, err := mfile.ReadDir(c, c.SourceDir+"/content/drafts")
		if err != nil {
			return err
		}
		articleSources = append(articleSources, drafts...)
	}

	for _, s := range articleSources {
		source := s

		c.Jobs <- func() error {
			article, err := renderArticle(c, source)
			if err != nil {
				return err
			}

			articles = append(articles, article)
			return nil
		}
	}

	//
	// Pages
	//

	var pagesMeta map[string]*Page
	pagesMetaUnchanged, err := myaml.ParseFile(c, c.SourceDir+"/pages/_meta.yaml", &pagesMeta)
	if err != nil {
		return err
	}

	// If the master metadata file changed, then any page could potentially
	// have changed, so we'll have to re-render all of them: pass a forced
	// context into each page job.
	pageContext := c
	if !pagesMetaUnchanged {
		c.ForcedContext()
	}

	pageSources, err := mfile.ReadDir(c, c.SourceDir+"/pages")
	if err != nil {
		return err
	}

	for _, s := range pageSources {
		source := s

		c.Jobs <- func() error {
			return renderPage(pageContext, pagesMeta, source)
		}
	}

	//
	// Phase 2
	//

	if !c.Wait() {
		return nil
	}

	return nil
}

//
// Structs
//

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

// publishingInfo produces a brief spiel about publication which is intended to
// go into the left sidebar when an article is shown.
func (a *Article) publishingInfo() string {
	return `<p><strong>Article</strong><br>` + a.Title + `</p>` +
		`<p><strong>Published</strong><br>` + a.PublishedAt.Format("January 2, 2006") + `</p> ` +
		`<p><strong>Location</strong><br>` + a.Location + `</p>` +
		twitterInfo
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

// Tag is a symbol assigned to an article to categorize it.
//
// This feature is not meanted to be overused. It's really just for tagging
// a few particular things so that we can generate content-specific feeds for
// certain aggregates (so far just Planet Postgres).
type Tag string

//
// Helpers
//

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

func renderArticle(c *modulr.Context, source string) (*Article, error) {
	// We can't really tell whether we need to rebuild our articles index, so
	// we always at least parse every article to get its metadata struct, and
	// then rebuild the index every time. If the source was unchanged though,
	// we stop after getting its metadata.
	forceC := c.ForcedContext()

	var article Article
	data, unchanged, err := myaml.ParseFileFrontmatter(forceC, source, &article)
	if err != nil {
		return nil, err
	}

	err = article.validate(source)
	if err != nil {
		return nil, err
	}

	// See comment above: we always parse metadata, but if the file was
	// unchanged, it's okay not to re-render it.
	if unchanged {
		// Decrement stats (which will have incremented due to the forced
		// context) on this sort of no-op to give a more realistic work
		// representation.
		c.Stats.SetJobSkipped()

		return &article, nil
	}

	article.Content = string(mmarkdown.Render(c, []byte(data)))
	article.Draft = strings.Contains(filepath.Base(filepath.Dir(source)), "drafts")
	article.Slug = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))

	article.TOC, err = mtoc.RenderFromHTML(article.Content)
	if err != nil {
		return nil, err
	}
	/*

		format, ok := pathAsImage(
			path.Join(sorg.ContentDir, "images", article.Slug, "hook"),
		)
		if ok {
			article.HookImageURL = "/assets/" + article.Slug + "/hook." + format
		}

		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}

		card := &twitterCard{
			Title:       article.Title,
			Description: article.Hook,
		}
		format, ok = pathAsImage(
			path.Join(sorg.ContentDir, "images", article.Slug, "twitter@2x"),
		)
		if ok {
			card.ImageURL = sorg.AbsoluteURL + "/assets/" + article.Slug + "/twitter@2x." + format
		}
	*/

	locals := getLocals(article.Title, map[string]interface{}{
		"Article":        article,
		"PublishingInfo": article.publishingInfo(),
		//"TwitterCard":    card,
	})

	err = mace.Render(c, MainLayout, ViewsDir+"/articles/show",
		path.Join(c.TargetDir, article.Slug), nil, locals)
	if err != nil {
		return nil, err
	}

	return &article, nil
}

func renderPage(c *modulr.Context, pagesMeta map[string]*Page, source string) error {
	// Strip the `.ace` extension. Ace adds its own when rendering, and we
	// don't want it on the output files.
	source = strings.TrimSuffix(source, path.Ext(source))

	// Remove the "./pages" directory, but keep the rest of the path.
	//
	// Looks something like "about".
	pagePath := strings.TrimPrefix(mfile.MustAbs(source), mfile.MustAbs("./pages"))

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
		return err
	}

	err = mace.Render(c, MainLayout, source, target, nil, locals)
	if err != nil {
		return err
	}

	return nil
}
