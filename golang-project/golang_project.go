// Copyright 2016 Google, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main // import "golang_project"

import (
	"fmt"
	"go/build"
	"io"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"text/template"
)

func usage() {
	log.Fatal(`Usage: gcr.io/cloud-builders/golang-project [FLAGS] [--] TARGET+

Where TARGET is the Go import path for a binary. At least one must be specified.

Where FLAGS may be
  --tag=TAG (required): The tag of the resulting image to be build.
  --base-image=BASE_IMAGE (default=alpine): The base image to use.
  --entrypoint=ENTRYPOINT: The binary to use as the entrypoint, if more than one
    is built.
  --skip-tests: Do not run unit tests before building the image.
`)
}

var dockerfileFormat = `FROM {{.BaseImage}}
ENV PATH=/golang_project_bin:$PATH
{{range .Binaries}}COPY {{.}} /golang_project_bin/
{{end}}ENTRYPOINT ["/golang_project_bin/{{.Entrypoint}}"]
`

var dockerfileTemplate = template.Must(template.New("dockerfile").Parse(dockerfileFormat))

func main() {
	log := log.New(os.Stderr, "", 0)

	cwd, _ := os.Getwd()

	baseImage := "alpine"
	entryPoint := ""
	skipTests := false
	tag := ""
	targets := []string{}

	// We aren't using the standard "flag" package here because it is
	// both unnecessarily restrictive (flags must come before positionals)
	// and unrestrictive (long flags can have double-dash). To promote
	// uniformity between different builders, many of which will not be
	// written in Go, a simple flag parsing scheme is used here.
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		// The tryFlag closure is a closure so that it can increment
		// the loop counter when it needs to advance arguments.
		tryFlag := func(flag string, value *string) bool {
			if args[i] == flag {
				i++
				*value = args[i]
				return true
			}
			if strings.HasPrefix(args[i], flag+"=") {
				*value = strings.TrimPrefix(args[i], flag+"=")
				return true
			}
			return false
		}

		if tryFlag("--base-image", &baseImage) {
			continue
		}
		if tryFlag("--entrypoint", &entryPoint) {
			continue
		}
		if tryFlag("--tag", &tag) {
			continue
		}
		if args[i] == "--skip-tests" {
			skipTests = true
			continue
		}
		if args[i] == "--" {
			targets = append(targets, args[i+1:]...)
			break
		}
		if strings.HasPrefix(args[i], "-") {
			usage()
		}
		targets = append(targets, args[i])
	}

	if tag == "" {
		log.Println("No --tag specified.")
		usage()
	}
	if len(targets) == 0 {
		log.Println("No targets specified.")
		usage()
	}

	binPaths := []string{}

	for _, target := range targets {
		pkg, err := build.Import(target, ".", 0)
		if err != nil {
			log.Fatalf("For target %q: %v", target, err)
		}

		if pkg.Name == "main" && entryPoint == "" {
			entryPoint = filepath.Base(pkg.ImportPath)
		}

		if !skipTests {
			cmd := exec.Command("go", "test", ".")
			cmd.Dir = pkg.Dir
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			fmt.Printf("In %q, running: go test .\n", cmd.Dir)
			if err := cmd.Run(); err != nil {
				log.Fatal()
			}
		}

		if pkg.Name != "main" {
			// We stop here after running 'go test' if it's not an executable target.
			continue
		}
		binPath := filepath.Join(pkg.BinDir, path.Base(pkg.ImportPath))
		if relBinPath, err := filepath.Rel(cwd, binPath); err == nil && !strings.HasPrefix(relBinPath, "..") {
			binPaths = append(binPaths, relBinPath)
		} else {
			log.Fatalf("For target %q: binary %q is built outside of the current directory.", target, relBinPath)
		}

		fmt.Printf("Running: go install %q\n", target)
		cmd := exec.Command("go", "install", target)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatal()
		}
	}

	if entryPoint == "" {
		log.Fatal("Could not infer entrypoint; either no targets were specified, or none of them were executable.")
	}

	fname := fmt.Sprintf("Dockerfile.%0000d", rand.Int63n(1e6))
	f, err := os.Create(fname)
	if err != nil {
		log.Fatalf("Could not create %q: %v", fname, err)
	}
	if err := dockerfileTemplate.Execute(f, struct {
		BaseImage  string
		Binaries   []string
		Entrypoint string
	}{
		BaseImage:  baseImage,
		Binaries:   binPaths,
		Entrypoint: entryPoint,
	}); err != nil {
		log.Fatalf("Could not write %q: %v", fname, err)
	}
	if err := f.Close(); err != nil {
		log.Fatalf("Could not close %q: %v", fname, err)
	}

	fin, err := os.Open(fname)
	if err != nil {
		log.Fatalf("Problem opening %q: %v\n", fname, err)
	}
	fmt.Printf("%q contents:\n", fname)
	io.Copy(os.Stdout, fin)

	fmt.Printf("Running: docker build --tag %q -f %q .\n", tag, fname)
	cmd := exec.Command("docker", "build", "--tag", tag, "-f", fname, ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Problem building image: %v", err)
	}
}
