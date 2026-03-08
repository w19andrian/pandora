package dnf

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"pandora/internal/providers"
	"pandora/internal/utils/shell"
)

const (
	pkgPrefix   = "Package::"
	queryFormat = pkgPrefix + "%{name}|%{epoch}|%{version}|%{release}|%{arch}\\n"
)

type DNF struct {
	name string
	dnf5 bool
}

type Options struct {
	AssumeNo  bool
	AssumeYes bool
	DryRun    bool // --setopt tsflags=test
}

type queryOps int

const (
	queryCheckAvailable queryOps = iota
	queryCheckInstalled
	queryCheckUpgrades
)

var queryOpsTable = map[queryOps][]string{
	queryCheckAvailable: {"--available", "--latest-limit=1"},
	queryCheckInstalled: {"--installed"},
	queryCheckUpgrades:  {"--upgrades"},
}

func New() *DNF {
	d := &DNF{}
	d.dnf5, d.name = d.isExist()

	return d
}

func (d *DNF) isExist() (bool, string) {
	dnf, err := exec.LookPath("dnf")
	if err != nil {
		return false, ""
	}

	out, err := exec.Command(dnf, "--version").CombinedOutput()
	if err != nil {
		return false, ""
	}

	return strings.Contains(string(out), "dnf5"), dnf
}

func (d *DNF) Install(ctx context.Context, pkgs []providers.Package, opts *Options) ([]providers.Package, error) {
	return install(ctx, pkgs, opts)
}

func (d *DNF) GetPackageInfo(ctx context.Context, pkgs []providers.Package) ([]providers.Package, error) {
	return getPackageInfo(ctx, pkgs)
}

func install(ctx context.Context, pkgs []providers.Package, opts *Options) ([]providers.Package, error) {
	args := []string{"install", "--assumeyes"}

	var toInstall []providers.Package
	for _, pkg := range pkgs {
		if !pkg.Status.Available && pkg.Status.Installed {
			continue
		}

		if pkg.Status.UpgradeAvailable {
			pkg.Spec = pkg.UpgradeSpec
			toInstall = append(toInstall, pkg)
		}

		toInstall = append(toInstall, pkg)
	}

	if len(toInstall) == 0 {
		return pkgs, errors.New("there is nothing to be installed at the moment")
	}

	rendered, err := renderPackages(toInstall, renderFullNEVRA)
	if err != nil {
		return pkgs, err
	}

	args = append(args, rendered...)

	_, err = run(ctx, args, opts)
	if err != nil {
		return pkgs, err
	}

	ts := time.Now()
	for _, pkg := range toInstall {
		pkg.Status.Installed = true
		pkg.Status.InstalledAt = &ts
	}

	return markInstalled(pkgs, toInstall), nil
}

func getPackageInfo(ctx context.Context, pkgs []providers.Package) ([]providers.Package, error) {
	installed, err := repoQuery(ctx, queryCheckInstalled, pkgs)
	if err != nil {
		return installed, err
	}

	available, err := repoQuery(ctx, queryCheckAvailable, installed)
	if err != nil {
		return available, err
	}

	return repoQuery(ctx, queryCheckUpgrades, available)
}

func repoQuery(ctx context.Context, qo queryOps, pkgs []providers.Package) ([]providers.Package, error) {
	args := []string{"repoquery", "--qf", queryFormat}
	args = append(args, queryOpsTable[qo]...)

	p, err := renderPackages(pkgs, renderNameOnly)
	if err != nil {
		return []providers.Package{}, err
	}

	args = append(args, p...)

	r, err := run(ctx, args, nil)
	if err != nil {
		return []providers.Package{}, err
	}

	out := packageParser(r)

	switch qo {
	case queryCheckAvailable:
		return markAvailable(pkgs, out), nil
	case queryCheckInstalled:
		return markInstalled(pkgs, out), nil
	case queryCheckUpgrades:
		return markUpgrades(pkgs, out), nil
	}

	return packageParser(r), nil
}

func run(ctx context.Context, args []string, o *Options) (string, error) {
	opts, err := buildOpts(o)
	if err != nil {
		return "", err
	}

	if len(opts) > 0 {
		args = append(args, opts...)
	}

	sh := shell.CmdSpec{Name: "dnf", Args: args}

	shellOpts := shell.DefaultRunOptions()

	run, err := sh.Run(ctx, shellOpts)
	if err != nil {
		return "", err
	}

	if run.ExitCode != 0 {
		return run.Output,
			fmt.Errorf("operation completed with error. exit code: %v", run.ExitCode)
	}

	return run.Output, nil
}

type pkgRenderer func(providers.PackageSpec) (string, error)

func renderPackages(pkgs []providers.Package, render pkgRenderer) ([]string, error) {
	out := make([]string, 0, len(pkgs))

	for _, v := range pkgs {
		p, err := render(v.Spec)
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}
	return out, nil
}

func renderNameOnly(pkg providers.PackageSpec) (string, error) {
	if pkg.Name == "" {
		return "", errors.New("name can not be empty")
	}

	return pkg.Name, nil
}

func renderNameVersion(pkg providers.PackageSpec) (string, error) {
	if pkg.Name == "" {
		return "", errors.New("name can not be empty")
	}

	if pkg.Version == "" {
		return pkg.Name, nil
	}

	return (pkg.Name + "-" + pkg.Version), nil
}

func renderFullNEVRA(pkg providers.PackageSpec) (string, error) {
	switch {
	case pkg.Name == "":
		return "", errors.New("invalid package format: missing package name")
	case pkg.Epoch == "":
		return "", errors.New("invalid package format: missing package epoch")
	case pkg.Version == "":
		return "", errors.New("invalid package format: missing package version")
	case pkg.Release == "":
		return "", errors.New("invalid package format: missing package release")
	case pkg.Arch == "":
		return "", errors.New("invalid package format: missing package arch")
	}

	return fmt.Sprintf("%s-%s:%s-%s.%s", pkg.Name, pkg.Epoch, pkg.Version, pkg.Release, pkg.Arch), nil
}

func packageParser(out string) []providers.Package {
	var pkgs []providers.Package

	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, pkgPrefix) {
			continue
		}

		trimmed := strings.TrimPrefix(line, pkgPrefix)
		parts := strings.Split(trimmed, "|")
		if len(parts) != 5 {
			panic(errors.New("mismatch between parsed output and struct field"))
		}

		var pkg providers.Package
		pkg.Spec.Name = parts[0]
		pkg.Spec.Epoch = parts[1]
		pkg.Spec.Version = parts[2]
		pkg.Spec.Release = parts[3]
		pkg.Spec.Arch = parts[4]

		pkgs = append(pkgs, pkg)
	}
	return pkgs
}

func buildOpts(o *Options) ([]string, error) {
	if o == nil {
		return []string{}, nil
	}

	if o.AssumeNo && o.AssumeYes {
		return nil, fmt.Errorf("AssumeYes and AssumeNo can not be true at the same time")
	}

	var opts []string

	if o.AssumeNo {
		opts = append(opts, "--assumeno")
	}

	if o.AssumeYes {
		opts = append(opts, "--assumeyes")
	}

	if o.DryRun {
		opts = append(opts, "--setopt", "tsflags=test")
	}

	return opts, nil
}

func markAvailable(request, available []providers.Package) []providers.Package {
	pkg := make(map[string]providers.Package, len(available))
	for _, av := range available {
		pkg[av.Spec.Name] = av
	}

	var out []providers.Package

	for _, req := range request {
		p, ok := pkg[req.Spec.Name]
		if !ok {
			req.Status.Available = false
			out = append(out, req)
			continue
		}

		if req.Status.Available {
			out = append(out, req)
			continue
		}

		p.Status.Available = true

		out = append(out, p)
	}

	return out
}

func markInstalled(request, installed []providers.Package) []providers.Package {
	pkg := make(map[string]providers.Package, len(installed))
	for _, p := range installed {
		pkg[p.Spec.Name] = p
	}

	out := make([]providers.Package, 0, len(request))

	for _, req := range request {
		inst, ok := pkg[req.Spec.Name]
		if !ok {
			req.Status.Installed = false
			out = append(out, req)
			continue
		}

		inst.Status.Installed = true
		inst.Status.Available = true
		out = append(out, inst)
	}

	return out
}

func markUpgrades(installed, upgrades []providers.Package) []providers.Package {
	upg := make(map[string]providers.Package)

	for _, u := range upgrades {
		upg[u.Spec.Name] = u
	}

	out := make([]providers.Package, 0, len(installed))

	for _, inst := range installed {
		available, ok := upg[inst.Spec.Name]
		if ok {
			inst.Status.UpgradeAvailable = true
			inst.UpgradeSpec = available.Spec
		}

		out = append(out, inst)
	}

	return out
}
