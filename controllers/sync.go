package controllers

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kstatus "sigs.k8s.io/kustomize/kstatus/status"
	kwait "sigs.k8s.io/kustomize/kstatus/wait"

	syncv1alpha1 "github.com/fluxcd/starling/api/v1alpha1"
)

// kubectl commands return a List if there's more than one result, but
// a single item if there's one result. This makes it infuriatingly
// fiddly to get a uniform representation out with JSONPath. The
// simplest thing seems to be to output both, and ignore `List`
// entries when parsing.
const itemJSONPath = `{.apiVersion} {.kind} {.metadata.name} {.metadata.namespace}{"\n"}`
const outputJSONPath = `jsonpath=` + itemJSONPath + `{range .items[*]}` + itemJSONPath + `{end}`

// This puts an order on status values, which I can use to calculate a
// _least_ status from a list of resources.
var statusRanks = map[kstatus.Status]int{
	kstatus.UnknownStatus:      0,
	syncv1alpha1.MissingStatus: 1,
	kstatus.FailedStatus:       2,
	kstatus.TerminatingStatus:  3,
	kstatus.InProgressStatus:   4,
	kstatus.CurrentStatus:      5,
}

// returns true if a is (strictly) less ready than b.
func lessReadyThan(a, b kstatus.Status) bool {
	return statusRanks[a] < statusRanks[b]
}

// dueOrWhen says if period has passed, give or take a second, and if
// not, when it will have passed.
func dueOrWhen(period time.Duration, now, last time.Time) (bool, time.Duration) {
	when := period - now.Sub(last)
	if when < time.Second { // close enough to not bother requeueing
		return true, 0
	}
	return false, when
}

// needsApply calculates whether a sync needs to run right now, and if
// not, how long until it does.
func needsApply(spec *syncv1alpha1.SyncSpec, status *syncv1alpha1.SyncStatus, now time.Time) (bool, time.Duration) {
	if status.LastApplySource == nil ||
		status.LastApplyTime == nil ||
		!(&spec.Source).Equiv(status.LastApplySource) {
		return true, 0
	}
	return dueOrWhen(spec.Interval.Duration, now, status.LastApplyTime.Time)
}

// needsStatus calculates whether the resources for a sync should be
// examined.
func needsStatus(status *syncv1alpha1.SyncStatus, now time.Time) (bool, time.Duration) {
	if status.LastResourceStatusTime == nil {
		return true, 0
	}
	return dueOrWhen(statusInterval, now, status.LastResourceStatusTime.Time)
}

// untargzip unpacks a gzipped-tarball. It uses a logger to report
// unexpected problems; mostly it will just return the error, on the
// basis that next time might yield a different result.
func untargzip(body io.Reader, tmpdir string, log logr.Logger) error {
	unzip, err := gzip.NewReader(body)
	if err != nil {
		return err
	}

	tr := tar.NewReader(unzip)
	numberOfFiles := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return err
		}

		// TODO symlinks, probably

		info := hdr.FileInfo()
		path := filepath.Join(tmpdir, hdr.Name)

		if info.IsDir() {
			// we don't need to create these since they will correspond to tmpdir
			if hdr.Name == "/" || hdr.Name == "./" {
				continue
			}
			if err = os.MkdirAll(path, info.Mode()&os.ModePerm); err != nil {
				log.Error(err, "failed to create directory while unpacking tarball", "path", path, "name", hdr.Name)
				return err
			}
			continue
		}

		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode())
		if err != nil {
			log.Error(err, "failed to create file while unpacking tarball", "path", path)
			return err
		}
		if _, err = io.Copy(f, tr); err != nil {
			log.Error(err, "failed to write file contents while unpacking tarball", "path", path)
			return err
		}
		_ = f.Close()
		numberOfFiles++
	}

	log.V(debug).Info("unpacked tarball", "tmpdir", tmpdir, "file-count", numberOfFiles)
	return nil
}

// unzip unpacks a ZIP archive. It follows the same logging rationale
// as untargzip.
func unzip(body io.Reader, tmpdir string, log logr.Logger) error {
	// The zip reader needs random access (that is
	// io.ReaderAt). Rather than try to do some tricky on-demand
	// buffering, I'm going to just read the whole lot into a
	// bytes.Buffer.
	buf := new(bytes.Buffer)
	size, err := io.Copy(buf, body)
	if err != nil {
		return err
	}

	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), size)
	if err != nil {
		return err
	}
	numberOfFiles := 0
	for _, file := range zipReader.File {
		name := file.FileHeader.Name
		// FIXME check for valid paths
		path := filepath.Join(tmpdir, name)

		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(path, os.FileMode(0700)); err != nil {
				log.Error(err, "failed to create directory from zip", "path", path)
				return err
			}
			continue
		}

		content, err := file.Open()
		if err != nil {
			log.Error(err, "failed to open file in zip", "path", path)
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(0600))
		if err != nil {
			log.Error(err, "failed to create file while unpacking zip", "path", path)
			return err
		}
		if _, err = io.Copy(f, content); err != nil {
			log.Error(err, "failed to write file contents while unpacking zip", "path", path)
			return err
		}
		_ = f.Close()
		content.Close()
		numberOfFiles++
	}

	log.V(debug).Info("unpacked zip", "tmpdir", tmpdir, "file-count", numberOfFiles)
	return nil
}

func updateResourcesStatus(ctx context.Context, client client.Reader, mapper meta.RESTMapper, status *syncv1alpha1.SyncStatus, now time.Time) {
	resources := status.Resources
	ids := make([]kwait.KubernetesObject, len(resources), len(resources))
	for i, resource := range resources {
		ids[i] = resource
	}

	resolver := kwait.NewResolver(client, mapper, 0)
	results := resolver.FetchAndResolveObjects(ctx, ids)
	// TODO this ignores errors (they just result in 'Unknown'
	// anyway)
	leastStatus := kstatus.CurrentStatus
	for i, result := range results {
		s := new(kstatus.Status)
		*s = result.Result.Status
		// https://github.com/kubernetes-sigs/kustomize/issues/2587
		// kstatus does not distinguish between Current (up to date)
		// and missing, other than in the (for humans) `Message`
		// field. Hence this brittle test:
		if result.Result.Message == "Resource does not exist" {
			*s = syncv1alpha1.MissingStatus
		}

		if lessReadyThan(*s, leastStatus) {
			leastStatus = *s
		}
		resources[i].Status = s
	}
	status.LastResourceStatusTime = &metav1.Time{Time: now}
	status.ResourcesLeastStatus = &leastStatus
}

type resolveDependency func(name string) (syncv1alpha1.SyncStatus, error)

// checkDependenciesReady looks at the dependencies of a Sync and sees
// if they are ready (have reached the given status). If not, it
// returns the list of dependencies not met yet.
func checkDependenciesReady(ctx context.Context, getdep resolveDependency, deps []syncv1alpha1.Dependency) ([]syncv1alpha1.Dependency, error) {
	var pending []syncv1alpha1.Dependency
	for _, dep := range deps {
		status, err := getdep(dep.SyncRef.Name)
		if err != nil {
			return nil, err
		}
		depStatus := status.ResourcesLeastStatus
		if depStatus == nil || lessReadyThan(*depStatus, dep.RequiredStatus) {
			// TODO: this is where I'd append the transitive
			// dependencies, so that cycles could be detected.
			pending = append(pending, dep)
		}
	}
	return pending, nil
}
