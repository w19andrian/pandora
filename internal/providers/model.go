package providers

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Package struct {
	Spec        PackageSpec
	UpgradeSpec PackageSpec
	Status      PackageStatus
}

type PackageSpec struct {
	Name    string
	Version string

	ExtraMetadata
}

type ExtraMetadata struct {
	Epoch   string
	Release string
	Arch    string
}

type PackageStatus struct {
	Available        bool
	Installed        bool
	UpgradeAvailable bool
	InstalledAt      *time.Time
	LastUpgrade      *time.Time
}

type Repository struct {
	Name string
	URL  string
}

type Operations interface {
	GetPackageStatus(ctx context.Context, pkgs []Package) ([]Package, error)
	Install(ctx context.Context, pkgs []Package) ([]Package, error)
}

type Options struct {
	AssumeNo  bool
	AssumeYes bool
	DryRun    bool // --setopt tsflags=test
}

func InitPackage(pkgSpec []PackageSpec) []Package {
	out := make([]Package, 0, len(pkgSpec))
	for _, p := range pkgSpec {
		var ps Package
		ps.Spec = p

		out = append(out, ps)
	}

	return out
}

type pkgRenderer func(PackageSpec) (string, error)

func RenderPackages(pkgs []Package, render pkgRenderer) ([]string, error) {
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

func RenderNameOnly(pkg PackageSpec) (string, error) {
	if pkg.Name == "" {
		return "", errors.New("name can not be empty")
	}

	return pkg.Name, nil
}

func RenderNameVersion(pkg PackageSpec) (string, error) {
	if pkg.Name == "" {
		return "", errors.New("name can not be empty")
	}

	if pkg.Version == "" {
		return pkg.Name, nil
	}

	return (pkg.Name + "-" + pkg.Version), nil
}

func RenderFullNEVRA(pkg PackageSpec) (string, error) {
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
