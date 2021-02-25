# Package format

This RFC describes a file format for "package files", and how it
controls syncing when it is present in the git repo structure.

## Motivation

[RFC 0002](0002-dependencies.md) describes the different ways a piece
of configuration can depend on another piece of configuration. In the
accompanying design, each Sync object can wait for the resources of
another Sync object to be ready.

However, it's not always, or even usually, desirable to express the
dependence in the syncing objects themselves. For instance, a piece of
configuration you import may have its own internal dependency
graph. To account for these using Sync objects you would need either

 - to have the syncs included (in which case, what source do they
   point to?); or,

 - for the import to _be_ the syncs, pointing at the original source
   (but then what if you want to adapt them?); or,

 - to break it up into syncs yourself, by looking into how the
   imported configuration is structured, possibly moving things
   around, and figuring out for yourself what depends on what.

It would be much better if those dependences internal to the imported
configuration were already specified within it, put there by the
author, who knows much more than you do about how the configuration is
supposed to work.

## Example

_TODO_

## Design

**Packages**

The fundamental idea here is to divide the objects to be synced into
packages, and let the packages direct how the objects are synced. Each
package is represented by a `Package` object which contains the
specification for syncing the package.

When reading objects from the filesystem, the presence of a "package
file" (tentatively named `Pkgfile`) in a directory makes that
directory its own package. Subdirectories are included in the package
recursively, unless they contain their own package file.

**Structure of a Package object**

The package object has apiVersion and objectMeta, so that it is
treatable with kustomize, kpt, and so on.

In its specification, it gives the dependences with other packages,
and the rules for checking its own health.

**Specifying dependence**

A natural way to mention a dependency is to say _this_ package depends
on _that_ package. There are circumstances in which this becomes
awkward, though. For example, if you have a configuration in which you
have:

 - custom resource definitions (CRDs)
 - controllers which rely on those CRDs
 - objects of the types defined in the CRDs

You might arrange these in directories thus:

```bash
$ tree ./config
config/
  crds/
  controllers/
  apps/
    app1/
    app2/
```

You would make `controllers/` a package that depends on `crds/`, and
make `apps/` a package that depends on `crds/` and `controllers/`. But
if you made `app1` its own package later, e.g., to define some health
checks, it would break the dependence unless you remembered also to
mention those dependencies.

<!-- IDEA --> **Pkgfile vs package objects**

A package file is not the same thing as a Package
object. A package file is located in a directory, and the directory
structure can have some meaning. So for instance, you could have

```bash
$ tree ./config
config/
  crds/
  controllers/
  apps/
    Pkgfile
    app1/
    app2/
```

where `apps/Pkgfile` says "everything below here depends on `crds/`
and `controllers/`". <!-- Does this break things? It might just be
confusing. -->

**Health checks**

_TODO_

### Additional considerations

**Using with kustomizations**

Ideally, you can just include a package file as a resource in a
kustomization. This does not play well with composing kustomizations,
because they might have their own package files, and it would not be
clear which resources belong to which packages.

Possible solutions:

 - annotate resources (using `commonAnnotations`) with their package
   name. This is extra work and makes composing kustomizations fiddly,
   but may be unavoidable.

**Using a Helm controller**

If you use an object to represent the installation of a Helm chart,
e.g., a `HelmRelease`, you need to look through that object to the
objects it represents, to see what you depend on. Just noting that the
`HelmRelease` object is created or ready is not adequate.

Possible solutions:

 - let the HelmRelease specify its health checks.
 - pair the HelmRelease (or releases) with a package file that
   mentions the expected resources. Tooling might help.
 - require Helm charts to be expanded in place.

### Alternatives considered

**Include syncs objects in distributable packages**

For example, you could include Kustomization (and possibly
HelmRelease) objects, from the [GitOps
Toolkit](https://fluxcd.io). These have a dependency mechanism that
operates between individual Kustomization object (and between
individual HelmRelease objects; but not between the two kinds).

There's one obvious hurdle to doing this: the sync objects must
include references to the git repository from whence they came. So
this would either have to rely on a convention (there's always a git
repository object named .. something that won't collide), or they
would have to be patched after the fact.

The latter is not so bad; a package manager of some kind could handle
it. If you wanted to refer to a package in place, rather than fetching
it to your own git repository, it would need to come with its own
GitRepository object. If you fetch it, you patch the GitRepository
object and any paths in Kustomizations.

One advantage of this approach is that you can have a package that
syncs a git repository (or Helm charts) elsewhere -- they don't have
to be in the package. In fact a package might refer _only_ to
definitions of syncs.

However, the big downside is that this is all tied to a particular
implementation (to wit: the GitOps Toolkit controllers), rather than
an algorithm.

**Describe dependencies in a higher-level object**

**Treat directories as indicating dependence**

**Have dependence relations only in syncs**

... But don't include them in distributable packages; i.e., the status
quo.

## Questions not addessed

To cover:

 - directory structure (Krmfile or equivalent marking boundaries)
   - observation: you tend to construct these depth-first
   - how do these line up with distribution and generation? (kpt, spresm)

 - operational data in a package file
   - upstream
   - version

 - metadata in a package file:
   - e.g., author, support email

 - declaring ordering dependencies amongst packages
   - given they are generally depth-first, ...?
   - declaring health-checks for a package
   - declare dependencies outside the mentioned packages

 - binding: requires/provides
   - how is this different to dependencies
   - environmental bindings
   - substitutions

