package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"time"

	"github.com/brandur/modulr"
	"github.com/brandur/modulr/mod/mfile"
	"github.com/brandur/modulr/mod/mmarkdown"
	"github.com/brandur/modulr/mod/myaml"
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
// Build function
//

func build(c *modulr.Context) error {
	c.Log.Debugf("Running build loop")

	//
	// Phase 0
	//

	c.Jobs <- func() error {
		return mfile.CopyFileToDir(c, c.SourceDir+"/hello.md", c.TargetDir)
	}

	var articles []*Article

	articleSources, err := mfile.ReadDir(c, c.SourceDir+"/content/articles")
	if err != nil {
		return err
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
	// Phase 1
	//

	c.Wait()

	// TODO: Error handling.

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

// Tag is a symbol assigned to an article to categorize it.
//
// This feature is not meanted to be overused. It's really just for tagging
// a few particular things so that we can generate content-specific feeds for
// certain aggregates (so far just Planet Postgres).
type Tag string

//
// Helpers
//

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
		return &article, nil
	}

	data = mmarkdown.Render(c, []byte(data))

	err = ioutil.WriteFile(c.TargetDir+"/"+filepath.Base(source), data, 0644)
	if err != nil {
		return nil, err
	}

	return &article, nil
}
