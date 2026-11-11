// Package diskuse works out where the server's disk goes and what a cleanup
// may delete. It decides only: the app layer feeds it what Docker and the
// database say (dockerx.Inventory, Facts) and runs the removals it picks.
//
// The rules, in short:
//   - Nothing a container uses is ever picked, nor the image of an app's live
//     version (a stopped app starts from it), nor anything of an app that is
//     deploying, nor anything made in the last RecentGuard (a build that just
//     finished and has no container yet).
//   - Volumes are only picked by the orphan-volume cleanup, and only bitploy
//     volumes of apps that no longer exist that no container mounts.
//   - Resources labelled for another bitploy installation on the same Docker
//     are never picked.
package diskuse

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
)

// RecentGuard: images and containers younger than this are never removed.
const RecentGuard = 30 * time.Minute

// HelperGuard: a stopped bitploy container that belongs to no app (a
// backup or file helper, the port proxy) is only removed after this long; a
// backup of a big volume can keep its helper for a while.
const HelperGuard = 6 * time.Hour

// Kinds of cleanup.
const (
	BuildCache        = "build_cache"        // the bitploy builder's cache and Docker's own build cache
	UnusedImages      = "unused_images"      // bitploy images without a tag or of deleted apps
	StoppedContainers = "stopped_containers" // bitploy containers that are no app's current one
	RollbackImages    = "rollback_images"    // older versions of apps, kept for rollback
	OtherImages       = "other_images"       // images on the host that are not bitploy's and that no container uses
	OrphanVolumes     = "orphan_volumes"     // bitploy volumes of deleted apps
)

// Kinds lists every cleanup kind.
var Kinds = []string{BuildCache, UnusedImages, StoppedContainers, RollbackImages, OtherImages, OrphanVolumes}

// AutoSteps is the order of the automatic cleanup: what costs nothing first,
// rollback versions and other software's images last. Volumes never.
var AutoSteps = []string{BuildCache, UnusedImages, StoppedContainers, RollbackImages, OtherImages}

// SafeSteps run when a build is refused for disk space: they only cost a
// slower next build.
var SafeSteps = []string{BuildCache, UnusedImages}

// Image categories.
const (
	ImageInUse    = "in_use"   // a container runs from it, or it is an app's live version
	ImageRollback = "rollback" // an older version of an app that exists
	ImageUnused   = "unused"   // bitploy's, without a tag or of a deleted app
	ImageOther    = "other"    // not bitploy's
)

// Container categories.
const (
	ContainerRunning  = "running"  // running, paused or restarting
	ContainerStopped  = "stopped"  // a stopped app's current container: starting the app needs it
	ContainerLeftover = "leftover" // a bitploy container that is no app's current one
	ContainerOther    = "other"    // not bitploy's
)

// Volume categories.
const (
	VolumeApp     = "app"     // /data or a service volume of an app that exists
	VolumeBuilder = "builder" // the bitploy builder's cache
	VolumeOrphan  = "orphan"  // bitploy's, of an app that no longer exists
	VolumeOther   = "other"   // not bitploy's
)

// App is an app that exists.
type App struct {
	ID, Slug, Name string
}

// Facts is what the database and the deploy runtime know.
type Facts struct {
	Instance string // this installation's id (nl.bitploy.instance)
	Apps     map[string]App
	// ImageApp maps every image reference a deployment of an existing app
	// refers to (bitploy/shop:12, postgres:16) to that app's id.
	ImageApp map[string]string
	// Protected are image references never to remove: every app's live
	// version and those of deployments in flight.
	Protected map[string]bool
	// CurrentContainers are the names of every app's current containers.
	CurrentContainers map[string]bool
	// ActiveDeployments and BusyApps are in flight right now.
	ActiveDeployments map[string]bool
	BusyApps          map[string]bool
	// AppVolumes maps volume names to the app (app_volumes rows).
	AppVolumes map[string]string
	// BuilderVolume is the builder's state volume.
	BuilderVolume string
	Now           time.Time
}

// Image is an image with what bitploy makes of it.
type Image struct {
	dockerx.InvImage
	Category  string
	AppID     string // the app it belongs to; "" for none or a deleted one
	InUse     bool   // a container uses it
	Unique    int64  // bytes that only this image holds
	Removable bool   // its category's cleanup removes it
}

// Container is a container with what bitploy makes of it.
type Container struct {
	dockerx.InvContainer
	Category  string
	AppID     string
	Removable bool
}

// Volume is a volume with what bitploy makes of it.
type Volume struct {
	dockerx.InvVolume
	Category  string
	AppID     string // the app it belongs to; for an orphan the deleted app's id
	Removable bool
}

// Report is the inventory sorted into categories.
type Report struct {
	Images     []Image     // largest first
	Containers []Container // largest first
	Volumes    []Volume    // largest first
	// SharedImageBytes is the part of the images' total in layers several
	// images share; it is not in any image's Unique.
	SharedImageBytes int64
	BuildCacheBytes  int64 // Docker's own build cache
}

// owner says whose a resource is by its labels: this installation's (or
// from before installation ids), another installation's, or nobody's.
func (f Facts) owner(labels map[string]string) (mine, other bool) {
	if labels[dockerx.LabelManaged] != "1" {
		return false, false
	}
	inst := labels[dockerx.LabelInstance]
	if inst == "" || inst == f.Instance {
		return true, false
	}
	return false, true
}

// NormalizeRef writes an image reference the way Docker lists tags:
// docker.io/library/postgres becomes postgres:latest.
func NormalizeRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "docker.io/")
	ref = strings.TrimPrefix(ref, "index.docker.io/")
	ref = strings.TrimPrefix(ref, "library/")
	if strings.Contains(ref, "@") {
		return ref
	}
	// A tag is a ":" after the last "/" (a registry host may have a port).
	if i := strings.LastIndexByte(ref, ':'); i < 0 || i < strings.LastIndexByte(ref, '/') {
		ref += ":latest"
	}
	return ref
}

func (f Facts) refs(im dockerx.InvImage) []string {
	out := make([]string, 0, len(im.Tags)+len(im.Digests)+1)
	for _, t := range im.Tags {
		out = append(out, NormalizeRef(t))
	}
	for _, d := range im.Digests {
		out = append(out, NormalizeRef(d))
	}
	return append(out, im.ID)
}

func uniqueBytes(im dockerx.InvImage) int64 {
	if im.SharedSize < 0 {
		return im.Size
	}
	if u := im.Size - im.SharedSize; u > 0 {
		return u
	}
	return 0
}

func (f Facts) recent(t time.Time) bool {
	return !t.IsZero() && f.Now.Sub(t) < RecentGuard
}

// ClassifyImage sorts one image. usedIDs are the image ids containers run
// from (in case the daemon did not count containers per image).
func (f Facts) ClassifyImage(im dockerx.InvImage, usedIDs map[string]bool) Image {
	out := Image{InvImage: im, Unique: uniqueBytes(im)}
	mine, otherInstall := f.owner(im.Labels)
	out.InUse = im.Containers > 0 || usedIDs[im.ID]

	appID := ""
	if mine {
		appID = im.Labels[dockerx.LabelAppID]
	}
	// The database viewer's helper image is bitploy's own tool, not an app's.
	protected := out.InUse || otherInstall || f.recent(im.Created) || mine && im.Labels[dockerx.LabelDBHelper] != ""
	for _, r := range f.refs(im) {
		if f.Protected[r] {
			protected = true
		}
		if id := f.ImageApp[r]; id != "" && appID == "" {
			appID = id
		}
	}
	if dep := im.Labels[dockerx.LabelDeployment]; mine && dep != "" && f.ActiveDeployments[dep] {
		protected = true
	}
	_, appExists := f.Apps[appID]
	if appExists && f.BusyApps[appID] {
		protected = true
	}
	if appExists {
		out.AppID = appID
	}

	switch {
	case !mine && appID == "":
		out.Category = ImageOther
	case out.InUse || f.isProtectedRef(im):
		out.Category = ImageInUse
	case appExists && len(im.Tags) > 0:
		out.Category = ImageRollback
	default:
		out.Category = ImageUnused
	}
	// While anything deploys, other software's images stay: an image a
	// deploy just pulled is not linked to its app yet, and its Created is
	// the upstream build date, so neither guard above would catch it.
	if out.Category == ImageOther && (len(f.ActiveDeployments) > 0 || len(f.BusyApps) > 0) {
		protected = true
	}
	out.Removable = !protected && out.Category != ImageInUse
	return out
}

func (f Facts) isProtectedRef(im dockerx.InvImage) bool {
	for _, r := range f.refs(im) {
		if f.Protected[r] {
			return true
		}
	}
	return false
}

// ClassifyContainer sorts one container.
func (f Facts) ClassifyContainer(c dockerx.InvContainer) Container {
	out := Container{InvContainer: c}
	mine, _ := f.owner(c.Labels)
	appID := c.Labels[dockerx.LabelAppID]
	if _, ok := f.Apps[appID]; ok && mine {
		out.AppID = appID
	}
	switch {
	case c.State == "running" || c.State == "paused" || c.State == "restarting" || c.State == "removing":
		out.Category = ContainerRunning
		if !mine {
			out.Category = ContainerOther
		}
	case !mine:
		out.Category = ContainerOther
	case f.CurrentContainers[c.Name]:
		out.Category = ContainerStopped
	default:
		out.Category = ContainerLeftover
		guard := RecentGuard
		if appID == "" {
			guard = HelperGuard
		}
		old := !c.Created.IsZero() && f.Now.Sub(c.Created) >= guard
		out.Removable = old && !f.ActiveDeployments[c.Labels[dockerx.LabelDeployment]] && !f.BusyApps[appID]
	}
	return out
}

// ClassifyVolume sorts one volume.
func (f Facts) ClassifyVolume(v dockerx.InvVolume) Volume {
	out := Volume{InvVolume: v}
	mine, _ := f.owner(v.Labels)
	labelApp := v.Labels[dockerx.LabelAppID]
	switch {
	case f.BuilderVolume != "" && v.Name == f.BuilderVolume:
		out.Category = VolumeBuilder
	case f.AppVolumes[v.Name] != "":
		out.Category, out.AppID = VolumeApp, f.AppVolumes[v.Name]
	case mine && labelApp != "":
		if _, ok := f.Apps[labelApp]; ok {
			out.Category, out.AppID = VolumeApp, labelApp
		} else {
			out.Category, out.AppID = VolumeOrphan, labelApp
			out.Removable = v.RefCount == 0
		}
	default:
		out.Category = VolumeOther
	}
	return out
}

// Classify sorts a whole inventory.
func (f Facts) Classify(inv dockerx.Inventory) Report {
	used := map[string]bool{}
	for _, c := range inv.Containers {
		if c.ImageID != "" {
			used[c.ImageID] = true
		}
	}
	r := Report{BuildCacheBytes: inv.BuildCacheBytes}
	var unique int64
	for _, im := range inv.Images {
		ci := f.ClassifyImage(im, used)
		r.Images = append(r.Images, ci)
		unique += ci.Unique
	}
	if inv.ImagesBytes > unique {
		r.SharedImageBytes = inv.ImagesBytes - unique
	}
	for _, c := range inv.Containers {
		r.Containers = append(r.Containers, f.ClassifyContainer(c))
	}
	for _, v := range inv.Volumes {
		r.Volumes = append(r.Volumes, f.ClassifyVolume(v))
	}
	sort.SliceStable(r.Images, func(i, j int) bool { return r.Images[i].Size > r.Images[j].Size })
	sort.SliceStable(r.Containers, func(i, j int) bool { return r.Containers[i].SizeRw > r.Containers[j].SizeRw })
	sort.SliceStable(r.Volumes, func(i, j int) bool { return r.Volumes[i].Size > r.Volumes[j].Size })
	return r
}

// Targets are what a cleanup of one kind removes.
type Targets struct {
	ImageRefs  []string // tags, or the id of an image without one
	Images     int      // images those refs remove
	Containers []string // container ids
	Volumes    []string // volume names
	Bytes      int64    // about what it frees (images: their unique bytes)
}

// imageCategory is the image category a cleanup kind removes.
func imageCategory(kind string) string {
	switch kind {
	case UnusedImages:
		return ImageUnused
	case RollbackImages:
		return ImageRollback
	case OtherImages:
		return ImageOther
	}
	return ""
}

// RemovableImages are the images a cleanup of kind removes (none for a
// kind that removes no images).
func (r Report) RemovableImages(kind string) []Image {
	cat := imageCategory(kind)
	if cat == "" {
		return nil
	}
	var out []Image
	for _, im := range r.Images {
		if im.Category == cat && im.Removable {
			out = append(out, im)
		}
	}
	return out
}

// RemovalOrder sorts images for removal, so a cleanup that stops as soon as
// enough is free takes the least valuable first: oldest first, and the
// newest rollback version of every app after everything else, so every app
// keeps a version to roll back to as long as possible.
func RemovalOrder(ims []Image) []Image {
	out := slices.Clone(ims)
	newest := map[string]time.Time{}
	for _, im := range out {
		if im.Category == ImageRollback && im.AppID != "" && im.Created.After(newest[im.AppID]) {
			newest[im.AppID] = im.Created
		}
	}
	last := func(im Image) bool {
		return im.Category == ImageRollback && im.AppID != "" && im.Created.Equal(newest[im.AppID])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if li, lj := last(out[i]), last(out[j]); li != lj {
			return lj
		}
		return out[i].Created.Before(out[j].Created)
	})
	return out
}

// Targets lists what a cleanup of kind would remove. Build cache has no
// targets: it is one prune call each (its Bytes come from the report).
func (r Report) Targets(kind string) Targets {
	var t Targets
	for _, im := range r.RemovableImages(kind) {
		t.Images++
		t.Bytes += im.Unique
		if len(im.Tags) == 0 {
			t.ImageRefs = append(t.ImageRefs, im.ID)
		} else {
			t.ImageRefs = append(t.ImageRefs, im.Tags...)
		}
	}
	switch kind {
	case StoppedContainers:
		for _, c := range r.Containers {
			if c.Category == ContainerLeftover && c.Removable {
				t.Containers = append(t.Containers, c.ID)
				t.Bytes += c.SizeRw
			}
		}
	case OrphanVolumes:
		for _, v := range r.Volumes {
			if v.Category == VolumeOrphan && v.Removable {
				t.Volumes = append(t.Volumes, v.Name)
				if v.Size > 0 {
					t.Bytes += v.Size
				}
			}
		}
	case BuildCache:
		t.Bytes = r.BuildCacheBytes
		for _, v := range r.Volumes {
			if v.Category == VolumeBuilder && v.Size > 0 {
				t.Bytes += v.Size
			}
		}
	}
	return t
}
