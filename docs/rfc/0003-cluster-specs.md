# Synchronising clusters, two ways

## Summary

Starling has a primitive `Sync` for declaring that a cluster API
`Cluster` should have a source synced to it; and, a mechanism
`SyncGroup` for declaring sets of syncs intensionally (using a label
selector).

In terms of managing cluster lifecycle using sources, there is further
to go. The Sync primitive does not say how any cluster is created;
and, it does not provide a location for the aggregate sync state of a
cluster to reside.

This RFC introduces new types (names TBC)

 - Bootstrap: for declaring how to create a cluster from sources;
 - ClusterSync: for declaring how a cluster should be synced

How these types interact with the existing types is also explored
below.
