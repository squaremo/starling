# A system for synchronising arbitrary numbers of clusters to git

## Summary

Tools like Flux do a solid job at synchronising a cluster with a git
repo -- that is, applying what's in the git repo to the cluster, to
maintain as close a correspondence as possible between them.

It is a pain to organise this to happen en-masse, if you have several
clusters, or several git repos. Other tools like ArgoCD support both
multiplex (one installation runs several syncs) and multitenant (allow
more than one sync in a cluster). But it is still incumbent on the
operator to arrange for these to happen.

Starling is a [Kubernetes operator][operator-defn] for managing the
synchronisation with git of arbitrary numbers of Kubernetes
clusters. The mode of syncing is expressed declaratively with
`SyncSet` resources, which tie a synchronisation source to a group of
clusters. The operator acts to ensure that all clusters of interest
are kept sycnronised with the git sources as specified.

[operator-defn]: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/

## Example



_Include here a brief, illustrative example of using `starling` in the
proposed way._

## Motivation

_Explain here the motivation for the change -- what problem are people
facing that is difficult to solve without `starling`?_

## Design

_Describe here the design of the change._

 - _What is the user interface or API? Give examples if you can._
 - _How will it work, technically?_
 _ _What are the corner cases, and how are they covered?_

## Backward compatibility

_Enumerate any backward-compatibility concerns:_

 - _Will people have to change their existing code?_
 - _If they don't, what might happen?_

_If the change will be backward-compatible, explain why._

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
