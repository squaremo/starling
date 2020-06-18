# A mechanism for dependence between syncs

## Summary

This RFC presents an extension to the syncing machinery to account for
relations between Syncs in which one Sync depends on others.

The initial design is this:

 - a Sync object can declare dependencies, which name another Sync and
   give a required status for it;
 - when a Sync is reconciled, if its dependencies do not have their
   required statuses, its application will be deferred.

## Motivation

In Kubernetes there are many ways an object can depend on another,
either by its definition (e.g., a Deployment that refers to a Secret),
or in its operation (a web service needs a database).

Because of the declarative design of Kubernetes, it is not usually
necessary _for correctness_ to order actions. However, careful
ordering can mean objects reach a ready state _more efficiently_
(e.g., with fewer retries), and in some cases, avoid blockages that
might otherwise need human intervention.

A secondary motivation is that defining relations can help the system
to better explain problems -- "this package says it depends on
something that isn't present".

## Design

### The shape of the problem

This section explains the problem that is to be solved. First and
foremost, the different kinds of relation need to be accounted for in
the controller.

#### Scenarios where dependence is a useful mechanism

**[COMPLETION]** I need the custom resource definitions to have been
created before I can create my custom resources

This is a direct tail-to-head dependence; the resources cannot be
defined until the Kubernetes API server knows about their
types. Retries will probably be enough to get things working
eventually.

The more general case is that the dependency must have finished
running before the dependent can be applied; for example, the
dependency is a job that creates certificates for use by the
dependent.

**[AVAILABILITY]** My app will start more quickly and smoothly if
`service A` is up and running before `service B` is started

This is a statement of the "happy path" -- things will work more
efficiently if done in a particular order. Restarts, and a modicum of
care taken with the app code, will sort this out eventually.

Note there is usually a similar relation when _updating_ parts of the
app; updating `service B` is going to restart it, so it's best if
`service A` is known to be running when that happens.

**[LAYER]** I need to make sure Istio mutating webhook is registered
and running before any pods are created, so it can give them all
sidecars

Istio works by adding a sidecar proxy to each pod to connect it to the
mesh. In this case, a missing dependency would mean incorrect
operation -- pods would not have the sidecar, and not be connected to
the mesh.

One differentiating property of this scenario is that the dependence
is imposed on all other syncs -- "before _any_ pods" -- which suggests
it needs to be outside any individual Sync declaration.

**[VERSION]** I need to update the database for my app before I can
update the web service

In this scenario, the dependency is of the web service _version_ on a
particular version of the database. This is different to just needing
the database to be up and running -- it may cause hard to remedy
problems, or at least an outage, if they are updated in the wrong
order.

Usually you would rely on either being clever in the app code -- a
backward compatible web service -- or out-of-band coordination (e.g.,
a human) to make things happen in the right order.

**[ENVIRONMENT]** I need Tekton to be running in the cluster for my
pipeline resources to be useful, but I can't or don't want to install
it just for my use

This is a requirement of the environment, rather than a direct
dependence on another component.

### The dependence mechanism

The basic idea of dependence-sensitive syncing is that a Sync object
declares its dependencies, and their required status. Before the sync
controller applies configuration, it checks the requirements, and
defers the sync if they are not met. In aggregate, this results in
syncs being applied in dependency-first order.

This addresses the relations [AVAILABILITY] and [COMPLETION] above;
the other kinds of relation need mechanisms that operate on a
different level to Sync objects, and may be covered in later designs.

There are two pieces of information needed to evaluate whether a Sync
object's dependences are ready: a statement of the dependency
requirements, and the aggregate status of each of the dependencies.

#### Declaring dependence

The statement of the dependencies naturally lives with the dependent,
and consists of the name of the dependency and its minimum required
status.

#### Aggregate status

A dependence is evaluated using the aggregate status of a Sync. The
aggregate status is the _least ready_ status of the individual
objects.

"Least ready" needs some explanation. Roughly, the interesting
statuses for the relations detailed above are "doesn't exist",
"exists", and "available". The statuses reported by Kubernetes don't
reflect these neat categories (and for some types aren't given at
all). The [kstatus library][kstatus] at least gives every object a
status from a small set of possibilities, which can be mapped to those
of interest, and given an order (so that, e.g., a status of `Current`
satisfies a requirement of "exists").

If part of a sync is not ready, then the whole thing should not be
considered ready. Thus, the aggregate status is the least of all the
individual statuses.

[kstatus]: https://github.com/kubernetes-sigs/kustomize/tree/master/kstatus

## Backward compatibility

Without dependencies delcared, the mechanism will just be bypassed.

Retrofitting dependence to syncs after the fact can be done
gradually.

## Alternative designs and anticipated questions

**Depending on individual objects vs depending on whole syncs**

Depending on individual objects is precise, and doesn't necessitate
other machinery. On the other hand, it does mean more work from the
user to figure out the names of things from elsewhere to depend on --
and to keep the references accurate.

Depending on other Syncs (or the sources of them) is less precise, and
needs a notion of sync-readiness or aggregate status to be invented or
co-opted into the sync controller.

Precision is important, because it is always a particular object that
is required, and the _relation must break_ if the object is no longer
present. With a less precise mechanism, it might be possible for the
system to press on in an incorrect state, because the aggregate status
is still calculated as ready.

It may also be the case that the dependency is _not_ provided by
another Sync, but is present in the cluster by some other means
(perhaps because it's a reflection of some external system).

Occasionally you might want just an overall ready signal from another
package, perhaps as an approximation to depending on several of the
objects within. To depend on an aggregate status, it should be
possible -- perhaps in the near future if not now -- to use an object
that represents the aggregation (the [Application
CRD][application-crd] is one candidate).

You might also want to refer to an object from another Sync without
knowing the object's runtime name -- for this purpose, in the future
there could be a name resolution process.

[application-crd]: https://github.com/kubernetes-sigs/application

On the other hand:

Packages are supposed to be self-contained, so if there are exact
dependencies, there has probably been a design mistake. Depending on
individual objects breaks the encapsulation of a package: if something
depends on a particular object, and it changes name, the relation will
break needlessly.

It is also simpler to have versioned dependencies if you are dealing
with Syncs, since they have the revision recorded whereas ordinary
objects won't, in general.

If you have objects as dependencies, a similar argument can be made
for objects as dependents; and that gets complicated to express, and
fiddly to enforce. It might make the process more efficient though,
since _some_ things would be able to proceed.

On balance, it seems reasonable to begin with dependence on aggregate
status (by naming a sync), and build precise mechanisms afterwards.

**Serialising syncs, then applying in order**

This design shuffles dependents toward the back by deferring anything
that has not had its dependencies met. An alternative would be to
serialise Sync objects according to dependence, and apply in that
order.

In the base case of nothing having been applied to a cluster, you
might start by serialising all Sync objects targetting the cluster --
triggered, say, by any Sync for the cluster arriving at the head of
the queue. You would need to wait for each object to reach the desired
state (or fail to do so by a given timeout) before proceeding to the
next (or failing).

This is tricky to fit to the reconciler model -- you would need to
lock the targetted cluster somehow, so that no other syncs were
attempted on it. And it ties up a goroutine, waiting for each of the
syncs to complete. In principle, you could parallelise syncing when
there are multiple paths through the dependency graph, with more
goroutines and synchronisation.

To recover concurrency without introducing locking, goroutines and
synchronisation, you could work in sympathy with the reconciler model
by queueing the dependencies to be synced. Once they have been
applied, you can come back and attempt the dependent sync.

Since any given dependency might take a while to reach the required
state, you might need to put off the dependent sync for
longer. Requeueing a sync after its dependencies also implicitly sorts
them, too, so no need to do that up front.

You have now arrived back at the design given in this RFC.

## Unresolved questions and future considerations

**Handling internal dependencies**

E.g., service A depends on service B, and both are part of the same
sync source. Mentioning service B as a requirement would mean the sync
never goes ahead, since this represents a cycle.

Internal dependencies need special handling, since they will alter
_how_ syncing is done, rather than _whether_ it is done or not.

**Version dependence**

You might want to rely on version updates happening in a particular
order, and that will be different in character to the order of
creation.

For example: suppose service A depends on a specific version of
service B. That means that before they are created, service B better
be at that version, or creation should fail.

Once they are running, service B needs to be available _and_ running
that particular version (or version range) for service A to .. what?
How should the sync controller handle that, from the starting point of
two already-running services?

I think this demonstrates I'm thinking about versioning the wrong way.

**Detecting cycles**

E.g, Sync A depends on Sync B depends on Sync C. Naively, none of them
would get synced, but since the consequences may be spread through the
log, it could be difficult to notice why.

One solution might be to record the dependence path: if Sync A depends
on Sync B, and Sync B depends on Sync C, then Sync A records that it's
waiting on Sync B and C; if a Sync sees its own name in that list,
there's a cycle.
