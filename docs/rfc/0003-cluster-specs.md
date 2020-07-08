# Bootstrapping clusters

## Summary

Starling has a primitive `Sync` for declaring that a cluster API
`Cluster` should have a source synced to it; and, a mechanism
`SyncGroup` for declaring sets of syncs intensionally (using a label
selector).

In terms of managing cluster lifecycle using sources, there is further
to go. The Sync primitive does not say how any cluster is created or
updated.

This RFC introduces new types of object (names TBC)

 - Bootstrap: for declaring how to create a cluster from sources;
 - ClusterSync: for declaring how a cluster should be synced

How these types interact with the existing types is explored below.

# Motivation



# Design

Two axes:

 - where the definition lives and is enacted;
 - whether the definition is particular to the targetted cluster, or
   common to many clusters

**Common definitions in workload cluster**

These are definitions that are for objects in workload clusters, but
are treated as a single installation into the whole set of clusters,
for the purpose of updates.

For example, I want Prometheus to be installed in every cluster. When
updating it, I want to roll out an update to all clusters in one go.

**Common definitions in management cluster**

For example, there may be a common definition of an app cluster (being
of a certain number of nodes, at a certain version, etc.). Changing
this should roll the changes out to each cluster that used this
definition.

Note, though, that:

 - clusters are not in general upgraded in-place;
 - usually the whole set of definitions must be customised for each
   cluster (so even common definitions will have to be duplicated, and
   likely adapted)

**Particular definitions in workload cluster**



**Particular definitions in management cluster**

# Open questions
