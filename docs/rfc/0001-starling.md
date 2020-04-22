# A system for synchronising arbitrary numbers of clusters to git

## Summary

Starling is a [Kubernetes operator][operator-defn] for managing the
synchronisation with git of arbitrary numbers of Kubernetes
clusters. The mode of syncing is expressed declaratively with `Sync`
and `SyncGroup` resources, which tie a synchronisation _source_ to a
cluster or group of clusters, respectively. The operator acts to
ensure that all clusters of interest are kept synchronised with the
git sources as specified.

[operator-defn]: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/

## Example

_Include here a brief, illustrative example of using `starling`._

## Motivation

Tools like Flux do a solid job at synchronising a cluster with a git
repo -- that is, applying what's in the git repo to the cluster, to
maintain as close a correspondence as possible between them.

It is a pain to organise this to happen en-masse, if you have several
clusters, or several git repos. Other tools like ArgoCD support
multiplex (one installation runs several syncs) and multitenant (allow
more than one sync in a cluster) configurations. But it is still
incumbent on a developer or operator to arrange things in the right
way.

It would be better to start with the assumption that people will have
arbitrary numbers of clusters that are not handled individually, and
follow the consequences through.

## Design

The base design is this:

 - a _Source_ makes a git repository, or part thereof, available in the
   cluster. The revision of the repository made available is
   calculated according to a policy given in the Source definition;
   e.g., it might be HEAD of master, or the tag with the highest
   version in a semver range;
 - a _Cluster_ defines a target cluster for synchronisation;
 - a _Sync_ defines a desired synchronisation by referring to a Source
   with a Cluster, and supplying parameters of the synchronisation
   process;
 - a _SyncGroup_ defines a set of clusters to be synchronised to a
   Source, by giving a [selector][k8s-selector].

There are two reconciliation loops:

 - the sync control loop makes sure each Sync is carried out, by
   applying the Source to the Cluster, and records the result in the
   Sync resource;
 - the syncgroup control loop makes sure there is a Sync representing
   each selected cluster and source for each SyncGroup, and records
   aggregate results in the SyncGroup status.

**Avoiding the thundering herd**

Each source update will result in hundreds (or whatever large number
sounds difficult) of syncs to do, and possibly hundreds of clusters
becoming unavailable. This needs to be done as a rolling update, with
a bound on the number of syncs underway at any one time. Jittering may
help in the case that a rolling update is _not_ desired, which may be
a policy choice.

**Pull vs Push**

You might ask "But isn't it better to (per Flux) pull configuration
into the cluster, rather than push it from outside?".

This is false dichotomy, though: the important boundary is inside your
system vs outside your system, i.e., between things under your control
and things not under your contorl. The management cluster is still
pulling from outside your system, _into_ your system, even though it
is applying configuration across clusters (_within_ your system).

[k8s-selector]: TODO

## Backward compatibility

Backward compatibility is not a concern within this project at the
starting point. However, it might be reasonably asked: how do you move
from using Flux to using this?

There are several inevitable changes:

 - syncing will not support the [.flux.yaml][dot-flux-yaml] mechanism;
   whatever is used there will need to be run in automation and
   committed as flat YAMLs, with the possible exception of Kustomize
   which might reasonably be supported "natively" by the sync
   controller;
 - there's no image update automation, that will have to come from
   somewhere else (but it doens't make sense to have each cluster do
   its own automation anyway);
 - 

[dot-flux-yaml]: TODO

## Drawbacks and limitations

_What are the drawbacks of adopting this proposal; e.g.,_

 - _Does it require more engineering work from users (for an
   equivalent result)?_
 - _Are there performance concerns?_
 - _Will it close off other possibilities?_
 - _Does it add significant complexity to the runtime or standard library?_

## Alternatives

_Explain other designs or formulations that were considered (including
doing nothing, if not already covered above), and why the proposed
design is superior._

## Unresolved questions

_Keep track here of questions that come up while this is a draft.
Ideally, there will be nothing unresolved by the time the RFC is
accepted. It is OK to resolve a question by explaining why it
does not need to be answered_ yet _._

**Matters yet unresolved**

 - Disaster recovery, what would this look like
 - You have to have the Cluster resources in the same control cluster,
   how much is this a limitation; perhaps they could be mirrored from
   elsewhere.
 - Should a SyncGroup be able to provide a template for more than one
   sync/source?

**Dealing with backpressure**

A change to the source may come in before all the clusters have been
synced to the last one. There's no in-band flow control -- the syncing
cannot stop changes being made upstream -- so in general, changes will
have to be skipped if they are not complete.

While the controller is _running_, it can keep a queue of changes to
be made, and otherwise proceed step-wise. However, if it's recovering
from an outage, the only state it'll have is the state of all the
syncs and syncgroups.

**Authenticating to clusters**

The Cluster API type `Cluster` provides API endpoints, but not
credentials to connect. These must be provided separately.

I think each cluster provider is going to have its own means of
obtaining credentials, e.g., EKS will use AWS IAM somehow, so possibly
this will have to be implemented per provider.

Backup plan: require an agent to be running in the cluster when
provisioned. This is one extra requirement of the cluster, and also
means establishing connectively back to the control cluster, both of
which would be a shame.
