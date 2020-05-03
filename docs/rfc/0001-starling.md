# A system for synchronising arbitrary numbers of clusters to git

## Summary

Starling is a [Kubernetes operator][operator-defn] for managing the
synchronisation with git of arbitrary sets of clusters. The mode of
syncing is expressed declaratively with `Sync` and `SyncGroup`
resources, which tie a _source_ of configuration to a cluster or
selection of clusters, respectively. The operator acts to ensure that
all clusters of interest are kept synchronised with the sources as
specified.

[operator-defn]: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/

## Motivation

Tools like Flux do a solid job at synchronising a cluster with a git
repo -- that is, applying what's in the git repo to the cluster, to
maintain as close a correspondence as possible between them. It is a
pain to organise this to happen en-masse, if you have several clusters
or several git repos.

Other tools like ArgoCD support multiplex (one installation runs
several syncs) and multitenant (allow more than one sync in a cluster)
configurations. But it is still incumbent on a developer or operator
to arrange things in the right way while clusters or repositories come
and go.

It would be better to start with the assumption that people will have
arbitrary numbers of clusters that are not handled individually, and
work from a declaration of the desired _relation_ of clusters and
sources.

## Design

The base design is as follows:

 - the system runs in a [management cluster][cluster-api-mgmt]
   alongside other cluster API machinery;
 - a _Source_ makes a git repository, or part thereof, available to be
   synced. The revision of the repository made available is calculated
   according to a policy given in the Source definition; e.g., it
   might be HEAD of master, or the tag with the highest version in a
   semver range;
 - a _Sync_ defines a desired synchronisation by referring to a Source
   and a [target cluster][cluster-api-target], and supplying
   parameters of the synchronisation process;
 - a _SyncGroup_ defines a set of clusters to be synchronised to a
   Source, using a [selector][k8s-selector] to select the clusters of
   interest, and a template for creating Sync objects.

[cluster-api-mgmt]: https://cluster-api.sigs.k8s.io/user/concepts.html#management-cluster
[cluster-api-target]: https://cluster-api.sigs.k8s.io/user/concepts.html#workloadtarget-cluster

There are two reconciliation loops:

 - the sync control loop makes sure each Sync is carried out, by
   applying the Source to the Cluster, and records the result in the
   Sync resource;

 - the syncgroup control loop makes sure there is a Sync representing
   each selected cluster and source for each SyncGroup, and records
   aggregate results in the SyncGroup status.

[k8s-selector]: https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/

## Backward compatibility

Backward compatibility is not a concern within this project, at the
starting point. However, it might be reasonably asked: how do you move
from using Flux or ArgoCD to using this?

There are several inevitable changes:

 - syncing will not support the [.flux.yaml][dot-flux-yaml] mechanism
   (or resource servers in ArgoCD, which similarly support different
   means of generating configuration).

   Whatever is used there will need to be run in automation and
   committed as flat YAMLs; with the possible exception of Kustomize
   which might reasonably be supported "natively" by the sync
   controller.

 - there's no image update automation, that will have to come from
   somewhere else (but it doens't make sense to have every cluster do
   automation anyway).

The single, in-cluster deployment (which ought to exist) should work
much the same as Flux or ArgoCD does now, otherwise.

[dot-flux-yaml]: https://docs.fluxcd.io/en/1.19.0/references/fluxyaml-config-files/

## Alternative designs and anticipated questions

**Management cluster as a single point of failure**

In this design above, the machinery all runs in a management cluster
alongside cluster definitions and so on. Implicitly this includes the
sync controller applying configuration to the remote clusters.

This makes the management cluster a single point of failure, for
updating configuration at least: if the management cluster has an
outage, workload clusters will continue running, but configuration
updates will not be applied.

An alternative design would be for the sync controller to run a sync
process in each workload cluster, and reproduce any sources and sync
definitions that are relevant. The trade-off, aside from additional
complexity, is that it is more difficult to accurately reflect the
state of the syncs in one place (i.e., the management cluster).

This would allow sources and sync processes to continue to run while
the management cluster is unavailable.

If there is any centralised control over syncing, as might be required
for incremental rollouts, then those would not make progress during a
management cluster outage (in either design). Overcoming that would
involve other means of co-ordination.

**Pull vs Push**

You might ask "But isn't it better (as per Flux) to pull configuration
into the cluster, rather than push it from outside?".

This is false dichotomy, though: the important boundary is inside your
system vs outside your system, i.e., between things under your control
and things not under your contorl. The management cluster is still
pulling from outside your system, _into_ your system, even though it
is applying configuration across clusters (_within_ your system).

Nonetheless there is a difference between pulling configuration from
each workload cluster, and pulling configuration into the management
cluster and applying it from there.

Aside from the differing failure modes (discussed under "Management
cluster as a single point of failure" above), it means `O(clusters)`
copies of the configuration. This is less of a concern for storage or
bandwidth than for complexity -- it means `O(clusters)` opportunities
for a partial failure. On the other hand, each application, wherever
it happens, is an opportunity for failure, so there's only a constant
factor difference.

## Unresolved questions and future considerations

**Rolling updates**

When rolling out a change, the syncgroup controller should act to
limit the ["blast radius"][blast-radius] of a bad change. This may be
by only updating one cluster at a time, or only some number or
proportion of clusters, or by some other policy.

A requirement that arises is that each _Sync_ may be pinned to a
particular revision; therefore, a _Source_ must be able to supply
arbitrary revisions on demand, since the various Syncs pointing at it
may be pinned to different revisions.

[blast-radius]: https://hello-world.sh/2018/12/31/application-architecture-patterns-cell-based-architecture.html

**Dealing with backpressure**

A change to the source may come in before all the clusters have been
synced to the last change. There's no in-band flow control -- the
syncing cannot stop changes being made upstream -- so in general,
changes will have to be skipped if they are not complete.

While the controller is _running_, it can keep a queue of changes to
be made, and otherwise proceed step-wise. However, if it's recovering
from an outage, the only state it'll have is the state of all the
syncs and syncgroups. Any rollout algorithm will have to take that
into account.

**Avoiding the thundering herd**

Each source update will result in hundreds (or whatever large number
sounds difficult) of syncs to do, and possibly hundreds of clusters
becoming unavailable. This needs to be done as a rolling update, with
a bound on the number of syncs underway at any one time. Jittering may
help in the case that a rolling update is _not_ desired, which may be
a policy choice.

**Matters yet unexamined**

 - disaster recovery
