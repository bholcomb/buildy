package workspace

import (
	"path/filepath"
	"slices"
	"testing"
)

// writeSharedLibraryLinks writes a workspace whose shared library mcore (module core) is linked
// by a shared library, plug (module plugin), and an executable, app (module app, which links
// plug too).
func writeSharedLibraryLinks(t *testing.T, root string) {
	t.Helper()
	writeFiles(t, root, map[string]string{
		"buildy.yaml":        "project: {name: ws}\n" + cppEnvironment + "workspace:\n  modules: [core, plugin, app]\n",
		"core/buildy.yaml":   "project: {name: core}\ntargets:\n  shared_libraries:\n    - {name: mcore, language: cpp, sources: [core.cpp]}\n",
		"plugin/buildy.yaml": "project: {name: plugin}\ntargets:\n  shared_libraries:\n    - {name: plug, language: cpp, sources: [plugin.cpp], libs: [mcore]}\n",
		"app/buildy.yaml":    "project: {name: app}\ntargets:\n  executables:\n    - {name: app, language: cpp, sources: [main.cpp], libs: [mcore, plug]}\n",
		"core/core.cpp":      "int core() { return 1; }",
		"plugin/plugin.cpp":  "int plugin() { return 2; }",
		"app/main.cpp":       "int main() {}",
	})
}

// sharedLibraryLinks generates the workspace of writeSharedLibraryLinks for a platform's debug
// build with a toolchain ("": the platform's default), and returns its output directory and tasks.
func sharedLibraryLinks(t *testing.T, platform, toolchain string) (string, []*BuildTask) {
	t.Helper()
	root := t.TempDir()
	writeSharedLibraryLinks(t, root)
	generated, err := generateWithToolchain(t, root, BuildContext{Platform: platform, Architecture: "x86_64", Configuration: "debug"}, toolchain)
	if err != nil {
		t.Fatal(err)
	}
	if err := graphOf(t, generated.Tasks...).Validate(); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "build", platform+"-x86_64-debug"), generated.Tasks
}

// wantArgs fails the test unless the link task of target runs args.
func wantArgs(t *testing.T, tasks []*BuildTask, target string, args ...string) {
	t.Helper()
	if got := linkTask(t, tasks, target).Command.Args; !slices.Equal(got, args) {
		t.Errorf("link of %s runs\n%q\nwant\n%q", target, got, args)
	}
}

// wantLinks fails the test unless the link task of target takes linked as an input, and not
// notLinked, and runs after the link task of library.
func wantLinks(t *testing.T, tasks []*BuildTask, target, library, linked, notLinked string) {
	t.Helper()
	link := linkTask(t, tasks, target)
	if !hasString(inputPaths(link), linked) {
		t.Errorf("link of %s lacks the input %s: %v", target, linked, inputPaths(link))
	}
	if notLinked != "" && hasString(inputPaths(link), notLinked) {
		t.Errorf("link of %s takes %s as an input: %v", target, notLinked, inputPaths(link))
	}
	if producer := linkTask(t, tasks, library); !hasString(link.Dependencies, producer.TaskID) {
		t.Errorf("link of %s does not run after %s: %v", target, producer.TaskID, link.Dependencies)
	}
}

// With MSVC a DLL is linked through its import library: an executable or a DLL that links a
// workspace DLL gets the import library's directory (lib/, while the DLLs are in bin/), and the
// import library its link writes as an input.
func TestMSVCLinksAWorkspaceDLLThroughItsImportLibrary(t *testing.T) {
	out, tasks := sharedLibraryLinks(t, "windows", "")
	bin, lib := filepath.Join(out, "bin"), filepath.Join(out, "lib")
	// The executable template's own ${output_dir}/lib and /bin, written with a slash on every host
	templateLib, templateBin := out+"/lib", out+"/bin"
	obj := func(module, target, source string) string {
		return filepath.Join(out, "obj", module, target, source+".obj")
	}

	if got, want := linkTask(t, tasks, "mcore").Outputs, []string{filepath.Join(bin, "mcore.dll"), filepath.Join(lib, "mcore.lib")}; !slices.Equal(got, want) {
		t.Errorf("link of mcore writes %v, want %v", got, want)
	}
	wantArgs(t, tasks, "plug", "link.exe", "/DLL", "/nologo", "/DEBUG", obj("plugin", "plug", "plugin.cpp"),
		"/LIBPATH:"+lib, "mcore.lib",
		"/OUT:"+filepath.Join(bin, "plug.dll"), "/IMPLIB:"+filepath.Join(lib, "plug.lib"))
	wantArgs(t, tasks, "app", "link.exe", "/nologo", "/DEBUG", obj("app", "app", "main.cpp"),
		"/LIBPATH:"+lib, "/LIBPATH:"+templateLib, "/LIBPATH:"+templateBin, "mcore.lib", "plug.lib",
		"/OUT:"+filepath.Join(bin, "app.exe"))

	wantLinks(t, tasks, "plug", "mcore", filepath.Join(lib, "mcore.lib"), filepath.Join(bin, "mcore.dll"))
	wantLinks(t, tasks, "app", "mcore", filepath.Join(lib, "mcore.lib"), filepath.Join(bin, "mcore.dll"))
	wantLinks(t, tasks, "app", "plug", filepath.Join(lib, "plug.lib"), filepath.Join(bin, "plug.dll"))
}

// MinGW's linker links a DLL itself (-lmcore finds mcore.dll), and buildy's MinGW toolchain writes
// no import library: the DLL in bin/ is what a link of it links.
func TestMinGWLinksAWorkspaceDLLItself(t *testing.T) {
	out, tasks := sharedLibraryLinks(t, "windows", "gcc-cpp-mingw")
	bin := filepath.Join(out, "bin")
	// The executable template's own ${output_dir}/lib and /bin, written with a slash on every host
	templateLib, templateBin := out+"/lib", out+"/bin"
	obj := func(module, target, source string) string {
		return filepath.Join(out, "obj", module, target, source+".o")
	}

	if got, want := linkTask(t, tasks, "mcore").Outputs, []string{filepath.Join(bin, "mcore.dll")}; !slices.Equal(got, want) {
		t.Errorf("link of mcore writes %v, want %v", got, want)
	}
	wantArgs(t, tasks, "plug", "x86_64-w64-mingw32-g++", "-shared", obj("plugin", "plug", "plugin.cpp"),
		"-L"+bin, "-lmcore", "-o", filepath.Join(bin, "plug.dll"))
	wantArgs(t, tasks, "app", "x86_64-w64-mingw32-g++", obj("app", "app", "main.cpp"),
		"-L"+bin, "-L"+templateLib, "-L"+templateBin, "-lmcore", "-lplug", "-o", filepath.Join(bin, "app.exe"))

	wantLinks(t, tasks, "plug", "mcore", filepath.Join(bin, "mcore.dll"), "")
	wantLinks(t, tasks, "app", "mcore", filepath.Join(bin, "mcore.dll"), "")
	wantLinks(t, tasks, "app", "plug", filepath.Join(bin, "plug.dll"), "")
}

// On Linux a shared library is linked itself, from bin/ (as it was before import libraries were
// link artefacts: these are the commands buildy 0.2.1 generated).
func TestLinuxLinksAWorkspaceSharedLibraryItself(t *testing.T) {
	out, tasks := sharedLibraryLinks(t, "linux", "")
	bin := filepath.Join(out, "bin")
	// The executable template's own ${output_dir}/lib and /bin, written with a slash on every host
	templateLib, templateBin := out+"/lib", out+"/bin"
	obj := func(module, target, source string) string {
		return filepath.Join(out, "obj", module, target, source+".o")
	}

	wantArgs(t, tasks, "plug", "g++", "-shared", "-Wl,-rpath,$ORIGIN", obj("plugin", "plug", "plugin.cpp"),
		"-L"+bin, "-lmcore", "-o", filepath.Join(bin, "libplug.so"))
	wantArgs(t, tasks, "app", "g++", "-Wl,-rpath,$ORIGIN", obj("app", "app", "main.cpp"),
		"-L"+bin, "-L"+templateLib, "-L"+templateBin, "-lmcore", "-lplug", "-o", filepath.Join(bin, "app"))

	wantLinks(t, tasks, "plug", "mcore", filepath.Join(bin, "libmcore.so"), "")
	wantLinks(t, tasks, "app", "mcore", filepath.Join(bin, "libmcore.so"), "")
	wantLinks(t, tasks, "app", "plug", filepath.Join(bin, "libplug.so"), "")
}
