// Copyright 2014 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bootstrap

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/pathtools"
)

const bootstrapDir = "$buildDir/.bootstrap"
const miniBootstrapDir = "$buildDir/.minibootstrap"

var (
	pctx = blueprint.NewPackageContext("github.com/google/blueprint/bootstrap")

	goTestMainCmd   = pctx.StaticVariable("goTestMainCmd", filepath.Join(bootstrapDir, "bin", "gotestmain"))
	chooseStageCmd  = pctx.StaticVariable("chooseStageCmd", filepath.Join(bootstrapDir, "bin", "choosestage"))
	pluginGenSrcCmd = pctx.StaticVariable("pluginGenSrcCmd", filepath.Join(bootstrapDir, "bin", "loadplugins"))

	compile = pctx.StaticRule("compile",
		blueprint.RuleParams{
			Command: "GOROOT='$goRoot' $compileCmd -o $out -p $pkgPath -complete " +
				"$incFlags -pack $in",
			Description: "compile $out",
		},
		"pkgPath", "incFlags")

	link = pctx.StaticRule("link",
		blueprint.RuleParams{
			Command:     "GOROOT='$goRoot' $linkCmd -o $out $libDirFlags $in",
			Description: "link $out",
		},
		"libDirFlags")

	goTestMain = pctx.StaticRule("gotestmain",
		blueprint.RuleParams{
			Command:     "$goTestMainCmd -o $out -pkg $pkg $in",
			Description: "gotestmain $out",
		},
		"pkg")

	pluginGenSrc = pctx.StaticRule("pluginGenSrc",
		blueprint.RuleParams{
			Command:     "$pluginGenSrcCmd -o $out $plugins",
			Description: "create $out",
		},
		"plugins")

	test = pctx.StaticRule("test",
		blueprint.RuleParams{
			Command:     "(cd $pkgSrcDir && $$OLDPWD/$in -test.short) && touch $out",
			Description: "test $pkg",
		},
		"pkg", "pkgSrcDir")

	cp = pctx.StaticRule("cp",
		blueprint.RuleParams{
			Command:     "cp $in $out",
			Description: "cp $out",
		},
		"generator")

	bootstrap = pctx.StaticRule("bootstrap",
		blueprint.RuleParams{
			Command:     "$bootstrapCmd -i $in -b $buildDir",
			Description: "bootstrap $in",
			Generator:   true,
		})

	chooseStage = pctx.StaticRule("chooseStage",
		blueprint.RuleParams{
			Command:     "$chooseStageCmd --current $current --bootstrap $bootstrapManifest -o $out $in",
			Description: "choosing next stage",
		},
		"current", "generator")

	touch = pctx.StaticRule("touch",
		blueprint.RuleParams{
			Command:     "touch $out",
			Description: "touch $out",
		},
		"depfile", "generator")

	// Work around a Ninja issue.  See https://github.com/martine/ninja/pull/634
	phony = pctx.StaticRule("phony",
		blueprint.RuleParams{
			Command:     "# phony $out",
			Description: "phony $out",
			Generator:   true,
		},
		"depfile")

	binDir     = pctx.StaticVariable("BinDir", filepath.Join(bootstrapDir, "bin"))
	minibpFile = filepath.Join("$BinDir", "minibp")

	docsDir = filepath.Join(bootstrapDir, "docs")

	primaryBuilderPlugins = []string{}
)

type bootstrapGoCore interface {
	BuildStage() Stage
	SetBuildStage(Stage)
}

func propagateStageBootstrap(mctx blueprint.TopDownMutatorContext) {
	if mod, ok := mctx.Module().(bootstrapGoCore); !ok || mod.BuildStage() != StageBootstrap {
		return
	}

	mctx.VisitDirectDeps(func(mod blueprint.Module) {
		if m, ok := mod.(bootstrapGoCore); ok {
			m.SetBuildStage(StageBootstrap)
		}
	})
}

func primaryBuilderPluginFinder(ctx blueprint.EarlyMutatorContext) {
	if pkg, ok := ctx.Module().(*goPackage); ok {
		if pkg.properties.Plugin {
			primaryBuilderPlugins = append(primaryBuilderPlugins, ctx.ModuleName())
		}
	}
}

type goPackageProducer interface {
	GoPkgRoot() string
	GoPackageTarget() string
}

func isGoPackageProducer(module blueprint.Module) bool {
	_, ok := module.(goPackageProducer)
	return ok
}

type goTestProducer interface {
	GoTestTarget() string
	BuildStage() Stage
}

func isGoTestProducer(module blueprint.Module) bool {
	_, ok := module.(goTestProducer)
	return ok
}

type goPluginProvider interface {
	GoPkgPath() string
	IsPlugin() bool
}

func isGoPlugin(module blueprint.Module) bool {
	if plugin, ok := module.(goPluginProvider); ok {
		return plugin.IsPlugin()
	}
	return false
}

func isBootstrapModule(module blueprint.Module) bool {
	_, isPackage := module.(*goPackage)
	_, isBinary := module.(*goBinary)
	return isPackage || isBinary
}

func isBootstrapBinaryModule(module blueprint.Module) bool {
	_, isBinary := module.(*goBinary)
	return isBinary
}

// A goPackage is a module for building Go packages.
type goPackage struct {
	properties struct {
		PkgPath  string
		Srcs     []string
		TestSrcs []string
		Plugin   bool
	}

	// The root dir in which the package .a file is located.  The full .a file
	// path will be "packageRoot/PkgPath.a"
	pkgRoot string

	// The path of the .a file that is to be built.
	archiveFile string

	// The path of the test .a file that is to be built.
	testArchiveFile string

	// The bootstrap Config
	config *Config

	// The stage in which this module should be built
	buildStage Stage
}

var _ goPackageProducer = (*goPackage)(nil)

func newGoPackageModuleFactory(config *Config) func() (blueprint.Module, []interface{}) {
	return func() (blueprint.Module, []interface{}) {
		module := &goPackage{
			buildStage: StagePrimary,
			config:     config,
		}
		return module, []interface{}{&module.properties}
	}
}

func (g *goPackage) GoPkgPath() string {
	return g.properties.PkgPath
}

func (g *goPackage) GoPkgRoot() string {
	return g.pkgRoot
}

func (g *goPackage) GoPackageTarget() string {
	return g.archiveFile
}

func (g *goPackage) GoTestTarget() string {
	return g.testArchiveFile
}

func (g *goPackage) BuildStage() Stage {
	return g.buildStage
}

func (g *goPackage) SetBuildStage(buildStage Stage) {
	g.buildStage = buildStage
}

func (g *goPackage) IsPlugin() bool {
	return g.properties.Plugin
}

func (g *goPackage) GenerateBuildActions(ctx blueprint.ModuleContext) {
	name := ctx.ModuleName()

	if g.properties.PkgPath == "" {
		ctx.ModuleErrorf("module %s did not specify a valid pkgPath", name)
		return
	}

	g.pkgRoot = packageRoot(ctx)
	g.archiveFile = filepath.Join(g.pkgRoot,
		filepath.FromSlash(g.properties.PkgPath)+".a")
	if len(g.properties.TestSrcs) > 0 && g.config.runGoTests {
		g.testArchiveFile = filepath.Join(testRoot(ctx),
			filepath.FromSlash(g.properties.PkgPath)+".a")
	}

	// We only actually want to build the builder modules if we're running as
	// minibp (i.e. we're generating a bootstrap Ninja file).  This is to break
	// the circular dependence that occurs when the builder requires a new Ninja
	// file to be built, but building a new ninja file requires the builder to
	// be built.
	if g.config.stage == g.BuildStage() {
		var deps []string

		if g.config.runGoTests {
			deps = buildGoTest(ctx, testRoot(ctx), g.testArchiveFile,
				g.properties.PkgPath, g.properties.Srcs, nil,
				g.properties.TestSrcs)
		}

		buildGoPackage(ctx, g.pkgRoot, g.properties.PkgPath, g.archiveFile,
			g.properties.Srcs, nil, deps)
	} else if g.config.stage != StageBootstrap {
		if len(g.properties.TestSrcs) > 0 && g.config.runGoTests {
			phonyGoTarget(ctx, g.testArchiveFile, g.properties.TestSrcs, nil, nil)
		}
		phonyGoTarget(ctx, g.archiveFile, g.properties.Srcs, nil, nil)
	}
}

// A goBinary is a module for building executable binaries from Go sources.
type goBinary struct {
	properties struct {
		Srcs           []string
		TestSrcs       []string
		PrimaryBuilder bool
	}

	// The path of the test .a file that is to be built.
	testArchiveFile string

	// The bootstrap Config
	config *Config

	// The stage in which this module should be built
	buildStage Stage
}

func newGoBinaryModuleFactory(config *Config, buildStage Stage) func() (blueprint.Module, []interface{}) {
	return func() (blueprint.Module, []interface{}) {
		module := &goBinary{
			config:     config,
			buildStage: buildStage,
		}
		return module, []interface{}{&module.properties}
	}
}

func (g *goBinary) GoTestTarget() string {
	return g.testArchiveFile
}

func (g *goBinary) BuildStage() Stage {
	return g.buildStage
}

func (g *goBinary) SetBuildStage(buildStage Stage) {
	g.buildStage = buildStage
}

func (g *goBinary) DynamicDependencies(ctx blueprint.DynamicDependerModuleContext) []string {
	if g.properties.PrimaryBuilder {
		return primaryBuilderPlugins
	}
	return []string{}
}

func (g *goBinary) GenerateBuildActions(ctx blueprint.ModuleContext) {
	var (
		name        = ctx.ModuleName()
		objDir      = moduleObjDir(ctx)
		archiveFile = filepath.Join(objDir, name+".a")
		aoutFile    = filepath.Join(objDir, "a.out")
		binaryFile  = filepath.Join("$BinDir", name)
		pluginSrc   = ""
		genSrcs     = []string{}
	)

	if len(g.properties.TestSrcs) > 0 && g.config.runGoTests {
		g.testArchiveFile = filepath.Join(testRoot(ctx), name+".a")
	}

	if g.properties.PrimaryBuilder && len(primaryBuilderPlugins) > 0 {
		pluginSrc = filepath.Join(objDir, "plugin.go")
		genSrcs = append(genSrcs, pluginSrc)
	}

	// We only actually want to build the builder modules if we're running as
	// minibp (i.e. we're generating a bootstrap Ninja file).  This is to break
	// the circular dependence that occurs when the builder requires a new Ninja
	// file to be built, but building a new ninja file requires the builder to
	// be built.
	if g.config.stage == g.BuildStage() {
		var deps []string

		if pluginSrc != "" {
			plugins := []string{}

			ctx.VisitDepsDepthFirstIf(isGoPlugin,
				func(module blueprint.Module) {
					plugin := module.(goPluginProvider)
					plugins = append(plugins, plugin.GoPkgPath())
				})

			ctx.Build(pctx, blueprint.BuildParams{
				Rule:      pluginGenSrc,
				Outputs:   []string{pluginSrc},
				Implicits: []string{"$pluginGenSrcCmd"},
				Args: map[string]string{
					"plugins": strings.Join(plugins, " "),
				},
			})
		}

		if g.config.runGoTests {
			deps = buildGoTest(ctx, testRoot(ctx), g.testArchiveFile,
				name, g.properties.Srcs, genSrcs, g.properties.TestSrcs)
		}

		buildGoPackage(ctx, objDir, name, archiveFile, g.properties.Srcs, genSrcs, deps)

		var libDirFlags []string
		ctx.VisitDepsDepthFirstIf(isGoPackageProducer,
			func(module blueprint.Module) {
				dep := module.(goPackageProducer)
				libDir := dep.GoPkgRoot()
				libDirFlags = append(libDirFlags, "-L "+libDir)
			})

		linkArgs := map[string]string{}
		if len(libDirFlags) > 0 {
			linkArgs["libDirFlags"] = strings.Join(libDirFlags, " ")
		}

		ctx.Build(pctx, blueprint.BuildParams{
			Rule:      link,
			Outputs:   []string{aoutFile},
			Inputs:    []string{archiveFile},
			Implicits: []string{"$linkCmd"},
			Args:      linkArgs,
		})

		ctx.Build(pctx, blueprint.BuildParams{
			Rule:    cp,
			Outputs: []string{binaryFile},
			Inputs:  []string{aoutFile},
		})
	} else if g.config.stage != StageBootstrap {
		if len(g.properties.TestSrcs) > 0 && g.config.runGoTests {
			phonyGoTarget(ctx, g.testArchiveFile, g.properties.TestSrcs, nil, nil)
		}

		intermediates := []string{aoutFile, archiveFile}
		phonyGoTarget(ctx, binaryFile, g.properties.Srcs, genSrcs, intermediates)
	}
}

func buildGoPackage(ctx blueprint.ModuleContext, pkgRoot string,
	pkgPath string, archiveFile string, srcs []string, genSrcs []string, orderDeps []string) {

	srcDir := moduleSrcDir(ctx)
	srcFiles := pathtools.PrefixPaths(srcs, srcDir)
	srcFiles = append(srcFiles, genSrcs...)

	var incFlags []string
	deps := []string{"$compileCmd"}
	ctx.VisitDepsDepthFirstIf(isGoPackageProducer,
		func(module blueprint.Module) {
			dep := module.(goPackageProducer)
			incDir := dep.GoPkgRoot()
			target := dep.GoPackageTarget()
			incFlags = append(incFlags, "-I "+incDir)
			deps = append(deps, target)
		})

	compileArgs := map[string]string{
		"pkgPath": pkgPath,
	}

	if len(incFlags) > 0 {
		compileArgs["incFlags"] = strings.Join(incFlags, " ")
	}

	ctx.Build(pctx, blueprint.BuildParams{
		Rule:      compile,
		Outputs:   []string{archiveFile},
		Inputs:    srcFiles,
		OrderOnly: orderDeps,
		Implicits: deps,
		Args:      compileArgs,
	})
}

func buildGoTest(ctx blueprint.ModuleContext, testRoot, testPkgArchive,
	pkgPath string, srcs, genSrcs, testSrcs []string) []string {

	if len(testSrcs) == 0 {
		return nil
	}

	srcDir := moduleSrcDir(ctx)
	testFiles := pathtools.PrefixPaths(testSrcs, srcDir)

	mainFile := filepath.Join(testRoot, "test.go")
	testArchive := filepath.Join(testRoot, "test.a")
	testFile := filepath.Join(testRoot, "test")
	testPassed := filepath.Join(testRoot, "test.passed")

	buildGoPackage(ctx, testRoot, pkgPath, testPkgArchive,
		append(srcs, testSrcs...), genSrcs, nil)

	ctx.Build(pctx, blueprint.BuildParams{
		Rule:      goTestMain,
		Outputs:   []string{mainFile},
		Inputs:    testFiles,
		Implicits: []string{"$goTestMainCmd"},
		Args: map[string]string{
			"pkg": pkgPath,
		},
	})

	libDirFlags := []string{"-L " + testRoot}
	ctx.VisitDepsDepthFirstIf(isGoPackageProducer,
		func(module blueprint.Module) {
			dep := module.(goPackageProducer)
			libDir := dep.GoPkgRoot()
			libDirFlags = append(libDirFlags, "-L "+libDir)
		})

	ctx.Build(pctx, blueprint.BuildParams{
		Rule:      compile,
		Outputs:   []string{testArchive},
		Inputs:    []string{mainFile},
		Implicits: []string{"$compileCmd", testPkgArchive},
		Args: map[string]string{
			"pkgPath":  "main",
			"incFlags": "-I " + testRoot,
		},
	})

	ctx.Build(pctx, blueprint.BuildParams{
		Rule:      link,
		Outputs:   []string{testFile},
		Inputs:    []string{testArchive},
		Implicits: []string{"$linkCmd"},
		Args: map[string]string{
			"libDirFlags": strings.Join(libDirFlags, " "),
		},
	})

	ctx.Build(pctx, blueprint.BuildParams{
		Rule:    test,
		Outputs: []string{testPassed},
		Inputs:  []string{testFile},
		Args: map[string]string{
			"pkg":       pkgPath,
			"pkgSrcDir": filepath.Dir(testFiles[0]),
		},
	})

	return []string{testPassed}
}

func phonyGoTarget(ctx blueprint.ModuleContext, target string, srcs []string,
	gensrcs []string, intermediates []string) {

	var depTargets []string
	ctx.VisitDepsDepthFirstIf(isGoPackageProducer,
		func(module blueprint.Module) {
			dep := module.(goPackageProducer)
			target := dep.GoPackageTarget()
			depTargets = append(depTargets, target)
		})

	moduleDir := ctx.ModuleDir()
	srcs = pathtools.PrefixPaths(srcs, filepath.Join("$srcDir", moduleDir))
	srcs = append(srcs, gensrcs...)

	ctx.Build(pctx, blueprint.BuildParams{
		Rule:      phony,
		Outputs:   []string{target},
		Inputs:    srcs,
		Implicits: depTargets,
	})

	// If one of the source files gets deleted or renamed that will prevent the
	// re-bootstrapping happening because it depends on the missing source file.
	// To get around this we add a build statement using the built-in phony rule
	// for each source file, which will cause Ninja to treat it as dirty if its
	// missing.
	for _, src := range srcs {
		ctx.Build(pctx, blueprint.BuildParams{
			Rule:    blueprint.Phony,
			Outputs: []string{src},
		})
	}

	// If there is no rule to build the intermediate files of a bootstrap go package
	// the cleanup phase of the primary builder will delete the intermediate files,
	// forcing an unnecessary rebuild.  Add phony rules for all of them.
	for _, intermediate := range intermediates {
		ctx.Build(pctx, blueprint.BuildParams{
			Rule:    blueprint.Phony,
			Outputs: []string{intermediate},
		})
	}

}

type singleton struct {
	// The bootstrap Config
	config *Config
}

func newSingletonFactory(config *Config) func() blueprint.Singleton {
	return func() blueprint.Singleton {
		return &singleton{
			config: config,
		}
	}
}

func (s *singleton) GenerateBuildActions(ctx blueprint.SingletonContext) {
	// Find the module that's marked as the "primary builder", which means it's
	// creating the binary that we'll use to generate the non-bootstrap
	// build.ninja file.
	var primaryBuilders []*goBinary
	// rebootstrapDeps contains modules that will be built in StageBootstrap
	var rebootstrapDeps []string
	// primaryRebootstrapDeps contains modules that will be built in StagePrimary
	var primaryRebootstrapDeps []string
	ctx.VisitAllModulesIf(isBootstrapBinaryModule,
		func(module blueprint.Module) {
			binaryModule := module.(*goBinary)
			binaryModuleName := ctx.ModuleName(binaryModule)
			binaryModulePath := filepath.Join("$BinDir", binaryModuleName)

			if binaryModule.BuildStage() == StageBootstrap {
				rebootstrapDeps = append(rebootstrapDeps, binaryModulePath)
			} else {
				primaryRebootstrapDeps = append(primaryRebootstrapDeps, binaryModulePath)
			}
			if binaryModule.properties.PrimaryBuilder {
				primaryBuilders = append(primaryBuilders, binaryModule)
			}
		})

	var primaryBuilderName, primaryBuilderExtraFlags string
	switch len(primaryBuilders) {
	case 0:
		// If there's no primary builder module then that means we'll use minibp
		// as the primary builder.  We can trigger its primary builder mode with
		// the -p flag.
		primaryBuilderName = "minibp"
		primaryBuilderExtraFlags = "-p"

	case 1:
		primaryBuilderName = ctx.ModuleName(primaryBuilders[0])

	default:
		ctx.Errorf("multiple primary builder modules present:")
		for _, primaryBuilder := range primaryBuilders {
			ctx.ModuleErrorf(primaryBuilder, "<-- module %s",
				ctx.ModuleName(primaryBuilder))
		}
		return
	}

	primaryBuilderFile := filepath.Join("$BinDir", primaryBuilderName)

	if s.config.runGoTests {
		primaryBuilderExtraFlags += " -t"
	}

	// Get the filename of the top-level Blueprints file to pass to minibp.
	topLevelBlueprints := filepath.Join("$srcDir",
		filepath.Base(s.config.topLevelBlueprintsFile))

	rebootstrapDeps = append(rebootstrapDeps, topLevelBlueprints)
	primaryRebootstrapDeps = append(primaryRebootstrapDeps, topLevelBlueprints)

	mainNinjaFile := filepath.Join(bootstrapDir, "main.ninja.in")
	mainNinjaTimestampFile := mainNinjaFile + ".timestamp"
	mainNinjaTimestampDepFile := mainNinjaTimestampFile + ".d"
	primaryBuilderNinjaFile := filepath.Join(bootstrapDir, "primary.ninja.in")
	primaryBuilderNinjaTimestampFile := primaryBuilderNinjaFile + ".timestamp"
	primaryBuilderNinjaTimestampDepFile := primaryBuilderNinjaTimestampFile + ".d"
	bootstrapNinjaFile := filepath.Join(bootstrapDir, "bootstrap.ninja.in")
	docsFile := filepath.Join(docsDir, primaryBuilderName+".html")

	primaryRebootstrapDeps = append(primaryRebootstrapDeps, docsFile)

	// If the tests change, be sure to re-run them. These need to be
	// dependencies for the ninja file so that it's updated after these
	// run. Otherwise we'd never leave the bootstrap stage, since the
	// timestamp file would be newer than the ninja file.
	ctx.VisitAllModulesIf(isGoTestProducer,
		func(module blueprint.Module) {
			testModule := module.(goTestProducer)
			target := testModule.GoTestTarget()
			if target != "" {
				if testModule.BuildStage() == StageBootstrap {
					rebootstrapDeps = append(rebootstrapDeps, target)
				} else {
					primaryRebootstrapDeps = append(primaryRebootstrapDeps, target)
				}
			}
		})

	switch s.config.stage {
	case StageBootstrap:
		// We're generating a bootstrapper Ninja file, so we need to set things
		// up to rebuild the build.ninja file using the primary builder.

		// BuildDir must be different between the three stages, otherwise the
		// cleanup process will remove files from the other builds.
		ctx.SetBuildDir(pctx, miniBootstrapDir)

		// Generate the Ninja file to build the primary builder. Save the
		// timestamps and deps, so that we can come back to this stage if
		// it needs to be regenerated.
		primarybp := ctx.Rule(pctx, "primarybp",
			blueprint.RuleParams{
				Command: fmt.Sprintf("%s --build-primary $runTests -m $bootstrapManifest "+
					"--timestamp $timestamp --timestampdep $timestampdep "+
					"-b $buildDir -d $outfile.d -o $outfile $in", minibpFile),
				Description: "minibp $outfile",
				Depfile:     "$outfile.d",
			},
			"runTests", "timestamp", "timestampdep", "outfile")

		args := map[string]string{
			"outfile":      primaryBuilderNinjaFile,
			"timestamp":    primaryBuilderNinjaTimestampFile,
			"timestampdep": primaryBuilderNinjaTimestampDepFile,
		}

		if s.config.runGoTests {
			args["runTests"] = "-t"
		}

		ctx.Build(pctx, blueprint.BuildParams{
			Rule:      primarybp,
			Outputs:   []string{primaryBuilderNinjaFile, primaryBuilderNinjaTimestampFile},
			Inputs:    []string{topLevelBlueprints},
			Implicits: rebootstrapDeps,
			Args:      args,
		})

		// Rebuild the bootstrap Ninja file using the minibp that we just built.
		// If this produces a difference, choosestage will retrigger this stage.
		minibp := ctx.Rule(pctx, "minibp",
			blueprint.RuleParams{
				Command: fmt.Sprintf("%s $runTests -m $bootstrapManifest "+
					"-b $buildDir -d $out.d -o $out $in", minibpFile),
				Description: "minibp $out",
				Generator:   true,
				Depfile:     "$out.d",
			},
			"runTests")

		args = map[string]string{}

		if s.config.runGoTests {
			args["runTests"] = "-t"
		}

		ctx.Build(pctx, blueprint.BuildParams{
			Rule:    minibp,
			Outputs: []string{bootstrapNinjaFile},
			Inputs:  []string{topLevelBlueprints},
			// $bootstrapManifest is here so that when it is updated, we
			// force a rebuild of bootstrap.ninja.in. chooseStage should
			// have already copied the new version over, but kept the old
			// timestamps to force this regeneration.
			Implicits: []string{"$bootstrapManifest", minibpFile},
			Args:      args,
		})

		// When the current build.ninja file is a bootstrapper, we always want
		// to have it replace itself with a non-bootstrapper build.ninja.  To
		// accomplish that we depend on a file that should never exist and
		// "build" it using Ninja's built-in phony rule.
		notAFile := filepath.Join(bootstrapDir, "notAFile")
		ctx.Build(pctx, blueprint.BuildParams{
			Rule:    blueprint.Phony,
			Outputs: []string{notAFile},
		})

		ctx.Build(pctx, blueprint.BuildParams{
			Rule:      chooseStage,
			Outputs:   []string{filepath.Join(bootstrapDir, "build.ninja.in")},
			Inputs:    []string{bootstrapNinjaFile, primaryBuilderNinjaFile},
			Implicits: []string{"$chooseStageCmd", "$bootstrapManifest", notAFile},
			Args: map[string]string{
				"current": bootstrapNinjaFile,
			},
		})

	case StagePrimary:
		// We're generating a bootstrapper Ninja file, so we need to set things
		// up to rebuild the build.ninja file using the primary builder.

		// BuildDir must be different between the three stages, otherwise the
		// cleanup process will remove files from the other builds.
		ctx.SetBuildDir(pctx, bootstrapDir)

		// We generate the depfile here that includes the dependencies for all
		// the Blueprints files that contribute to generating the big build
		// manifest (build.ninja file).  This depfile will be used by the non-
		// bootstrap build manifest to determine whether it should touch the
		// timestamp file to trigger a re-bootstrap.
		bigbp := ctx.Rule(pctx, "bigbp",
			blueprint.RuleParams{
				Command: fmt.Sprintf("%s %s -m $bootstrapManifest "+
					"--timestamp $timestamp --timestampdep $timestampdep "+
					"-b $buildDir -d $outfile.d -o $outfile $in", primaryBuilderFile,
					primaryBuilderExtraFlags),
				Description: fmt.Sprintf("%s $outfile", primaryBuilderName),
				Depfile:     "$outfile.d",
			},
			"timestamp", "timestampdep", "outfile")

		ctx.Build(pctx, blueprint.BuildParams{
			Rule:      bigbp,
			Outputs:   []string{mainNinjaFile, mainNinjaTimestampFile},
			Inputs:    []string{topLevelBlueprints},
			Implicits: primaryRebootstrapDeps,
			Args: map[string]string{
				"timestamp":    mainNinjaTimestampFile,
				"timestampdep": mainNinjaTimestampDepFile,
				"outfile":      mainNinjaFile,
			},
		})

		// Generate build system docs for the primary builder.  Generating docs reads the source
		// files used to build the primary builder, but that dependency will be picked up through
		// the dependency on the primary builder itself.  There are no dependencies on the
		// Blueprints files, as any relevant changes to the Blueprints files would have caused
		// a rebuild of the primary builder.
		bigbpDocs := ctx.Rule(pctx, "bigbpDocs",
			blueprint.RuleParams{
				Command: fmt.Sprintf("%s %s -b $buildDir --docs $out %s", primaryBuilderFile,
					primaryBuilderExtraFlags, topLevelBlueprints),
				Description: fmt.Sprintf("%s docs $out", primaryBuilderName),
			})

		ctx.Build(pctx, blueprint.BuildParams{
			Rule:      bigbpDocs,
			Outputs:   []string{docsFile},
			Implicits: []string{primaryBuilderFile},
		})

		// Detect whether we need to rebuild the primary stage by going back to
		// the bootstrapper. If this is newer than the primaryBuilderNinjaFile,
		// then chooseStage will trigger a rebuild of primaryBuilderNinjaFile by
		// returning to the bootstrap stage.
		ctx.Build(pctx, blueprint.BuildParams{
			Rule:      touch,
			Outputs:   []string{primaryBuilderNinjaTimestampFile},
			Implicits: rebootstrapDeps,
			Args: map[string]string{
				"depfile":   primaryBuilderNinjaTimestampDepFile,
				"generator": "true",
			},
		})

		// When the current build.ninja file is a bootstrapper, we always want
		// to have it replace itself with a non-bootstrapper build.ninja.  To
		// accomplish that we depend on a file that should never exist and
		// "build" it using Ninja's built-in phony rule.
		notAFile := filepath.Join(bootstrapDir, "notAFile")
		ctx.Build(pctx, blueprint.BuildParams{
			Rule:    blueprint.Phony,
			Outputs: []string{notAFile},
		})

		ctx.Build(pctx, blueprint.BuildParams{
			Rule:      chooseStage,
			Outputs:   []string{filepath.Join(bootstrapDir, "build.ninja.in")},
			Inputs:    []string{bootstrapNinjaFile, primaryBuilderNinjaFile, mainNinjaFile},
			Implicits: []string{"$chooseStageCmd", "$bootstrapManifest", notAFile, primaryBuilderNinjaTimestampFile},
			Args: map[string]string{
				"current": primaryBuilderNinjaFile,
			},
		})

		// Create this phony rule so that upgrades don't delete these during
		// cleanup
		ctx.Build(pctx, blueprint.BuildParams{
			Rule:    blueprint.Phony,
			Outputs: []string{bootstrapNinjaFile},
		})

	case StageMain:
		ctx.SetBuildDir(pctx, "${buildDir}")

		// We're generating a non-bootstrapper Ninja file, so we need to set it
		// up to re-bootstrap if necessary. We do this by making build.ninja.in
		// depend on the various Ninja files, the source build.ninja.in, and
		// on the timestamp files.
		//
		// The timestamp files themselves are set up with the same dependencies
		// as their Ninja files, including their own depfile. If any of the
		// dependencies need to be updated, we'll touch the timestamp file,
		// which will tell choosestage to switch to the stage that rebuilds
		// that Ninja file.
		ctx.Build(pctx, blueprint.BuildParams{
			Rule:      touch,
			Outputs:   []string{primaryBuilderNinjaTimestampFile},
			Implicits: rebootstrapDeps,
			Args: map[string]string{
				"depfile":   primaryBuilderNinjaTimestampDepFile,
				"generator": "true",
			},
		})

		ctx.Build(pctx, blueprint.BuildParams{
			Rule:      touch,
			Outputs:   []string{mainNinjaTimestampFile},
			Implicits: primaryRebootstrapDeps,
			Args: map[string]string{
				"depfile":   mainNinjaTimestampDepFile,
				"generator": "true",
			},
		})

		ctx.Build(pctx, blueprint.BuildParams{
			Rule:      chooseStage,
			Outputs:   []string{filepath.Join(bootstrapDir, "build.ninja.in")},
			Inputs:    []string{bootstrapNinjaFile, primaryBuilderNinjaFile, mainNinjaFile},
			Implicits: []string{"$chooseStageCmd", "$bootstrapManifest", primaryBuilderNinjaTimestampFile, mainNinjaTimestampFile},
			Args: map[string]string{
				"current":   mainNinjaFile,
				"generator": "true",
			},
		})

		// Create this phony rule so that upgrades don't delete these during
		// cleanup
		ctx.Build(pctx, blueprint.BuildParams{
			Rule:    blueprint.Phony,
			Outputs: []string{mainNinjaFile, docsFile},
		})

		if primaryBuilderName == "minibp" {
			// This is a standalone Blueprint build, so we copy the minibp
			// binary to the "bin" directory to make it easier to find.
			finalMinibp := filepath.Join("$buildDir", "bin", primaryBuilderName)
			ctx.Build(pctx, blueprint.BuildParams{
				Rule:    cp,
				Inputs:  []string{primaryBuilderFile},
				Outputs: []string{finalMinibp},
			})
		}
	}

	ctx.Build(pctx, blueprint.BuildParams{
		Rule:      bootstrap,
		Outputs:   []string{"$buildDir/build.ninja"},
		Inputs:    []string{filepath.Join(bootstrapDir, "build.ninja.in")},
		Implicits: []string{"$bootstrapCmd"},
	})
}

// packageRoot returns the module-specific package root directory path.  This
// directory is where the final package .a files are output and where dependant
// modules search for this package via -I arguments.
func packageRoot(ctx blueprint.ModuleContext) string {
	return filepath.Join(bootstrapDir, ctx.ModuleName(), "pkg")
}

// testRoot returns the module-specific package root directory path used for
// building tests. The .a files generated here will include everything from
// packageRoot, plus the test-only code.
func testRoot(ctx blueprint.ModuleContext) string {
	return filepath.Join(bootstrapDir, ctx.ModuleName(), "test")
}

// moduleSrcDir returns the path of the directory that all source file paths are
// specified relative to.
func moduleSrcDir(ctx blueprint.ModuleContext) string {
	return filepath.Join("$srcDir", ctx.ModuleDir())
}

// moduleObjDir returns the module-specific object directory path.
func moduleObjDir(ctx blueprint.ModuleContext) string {
	return filepath.Join(bootstrapDir, ctx.ModuleName(), "obj")
}
