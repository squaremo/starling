# Sync primitives

This RFC describes primitives for synchronisation to local and remote
clusters. The primitives are:

 - Sync, a sync to the local cluster;
 - RemoteSync, a directly-applied sync to a remote cluster; and,
 - ProxySync, which maintains a Sync in a remote cluster.

The idea is that these are a sufficient set of building blocks for
higher-level synchronisation operations, such as bootstrapping,
registration, and rollouts.

## Motivation

A system that deals with arbitrary numbers of clusters will
nonetheless need to operate on individual clusters. The high-level
operations of bootstrapping, registration , and rollouts all come with
different requirements for synchronisation.

**Bootstrapping**

The goal of bootstrapping is to construct a self-sustaining
system. For driving synchronisation from a management cluster, this
means that the controllers should be installed and under a syncing
regime, in the management cluster. This needs the most basic mode of
syncing: applying configuration to the local cluster.

**Registration**

Registration is bringing a remote ("downstream") cluster under
management. Registration can either be driven from the management
cluster, or from the downstream cluster. In the first case, the
management cluster needs to impose some configuration on the
downstream cluster, e.g., installing the controllers.

**Rollouts**

Rollouts are deploying new configuration to a set of clusters. This
can be accomplished by directly applying configuration to downstream
clusters, from the management cluster.

However, to lessen the "blast radius" of a management cluster failure,
it's desirable to make downstream clusters as self-sufficient as
possible. One way is to sync _indirectly_, by maintaining a sync local
to the downstream cluster, which can run independently of the
management cluster, but be updated and monitored from the management
cluster. This is called here a "proxy sync".

**The missing sync**

There's one flavour that is not represented here -- a sync that
applies configuration locally, but gets its specification from
elsewhere. This is the "pull" version of a proxy sync.

I have left it out for now because I am concentrating on a
"push"-style architecture. But it would be needed for scenarios in
which you want downstream clusters to connect upstream, or otherwise
for connections to be **in**bound to the management cluster, rather
than outbound.

## Design and discussion

**RBAC**

Each kind of sync is its own type, so that they can be dealt with
separately in RBAC permissions.

**Dependencies**

In general, there's no reason to not let any kind of sync depend on
any other that it can see (i.e., that's in the same namespace of the
same cluster).

The most obvious utility is having Syncs depend on other Syncs, the
syncs applying to other clusters (RemoteSync and ProxySync) have
dependences amongst themselves. But it's possible that it would also
be useful to Sync locally when a remote sync is ready, or vice versa.

For higher-level types, keeping dependencies in order will be a major
concern. For instance, you would want to prevent a situation in which
you have a ProxySync applying to a remote cluster without applying its
dependencies.
