package main

import (
	"github.com/brandur/modulir"
	"github.com/brandur/modulir/modules/mfile"
)

//////////////////////////////////////////////////////////////////////////////
//
//
//
// Build function
//
//
//
//////////////////////////////////////////////////////////////////////////////

func build(c *modulir.Context) []error {
	//
	// Phase 0: Setup
	//
	// (No jobs should be enqueued here.)
	//

	c.Log.Debugf("Running build loop")

	//
	// PHASE 1
	//

	{
		commonDirs := []string{
			c.TargetDir,
			c.TargetDir + "/hello",
		}
		for _, dir := range commonDirs {
			err := mfile.EnsureDir(c, dir)
			if err != nil {
				return []error{err}
			}
		}
	}

	{
		c.AddJob("hello", func() (bool, error) {
			source := c.SourceDir + "/content/hello.html"
			target := c.TargetDir + "/hello/index.html"

			sourceChanged := c.Changed(source)
			if !sourceChanged && !c.Forced() {
				return false, nil
			}

			return true, mfile.CopyFile(c, source, target)
		})
	}

	return nil
}
