# A mechansim for being sensitive to dependence

## Summary

This RFC presents an extension to the syncing machinery to account for
relations between Syncs in which one Sync depend on another in
some way.

TODO give outline of design here too

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

#### Hard vs soft dependence

In some cases the dependency is required for correct operation. For
example, if a Deployment uses a secret for environment variables, the
secret must exist before the pods started by the deployment can run.

This is hard dependence.

In some cases, it's expedient if the dependency is met before the
dependent needs it, but it will work eventually. For example, if
service A needs to connect to service B in the course of serving
requests, it won't be available until service B is available -- but it
is OK if service B starts after service A, or can reach a ready state
after service A.

This is soft dependence.

TODO why does this distinction matter? (A: at least because soft
dependence can be broken if necessary to satisfy hard dependence)

#### Transitions rely on states

A dependence is always in the form of a _transition_ relying on a
_state_; for example, service A cannot become available without
service B being available. In other words, it says what the controller
needs to observe about the dependency before it acts (usually by
applying an update).

Key: `state of dependency <-- transition for dependent`

 * **Defined <-- Available**

The dependent needs the dependency to have been defined (created)
before it can run. For example, a Deployment that mounts a ConfigMap.

In most situations, retries will sort this out, but the happy path is
to wait for the dependency to exist before applying the dependent.

 * **Available <-- Available**

The dependent needs the dependency to be _available_ for it to reach
an available state itself. For example, my web service needs the
database to be available to be able to serve records.

The efficient, happy path is that the dependency reaches a ready state
before the dependent needs it (which might otherwise be delayed by
restarts). This is a soft dependence, so a best effort is fine. In
practice this may mean either waiting to make sure the dependency
exists before applying the dependent, or it may mean going ahead in
the expectation that it will appear at some point.

 * **Completed <-- Defined**

The dependent needs the dependency to have reached a certain point, to
have enough information to be defined itself. For example, a webhook
needs certificates to be created and signed, which it can then use to
run.

Note that the completion may be something other than a process exiting
-- it could be an object that gets created or updated as part of a
service starting up.

This is a harder relation than `Available <-- Available`, since it
means applying the dependent _must_ be delayed until the condition is
met. It's also important to note that completion is a one-way gate --
once it is reached, the dependency is fulfilled.

 * **Available <-- Defined**

The dependent needs the dependency to be running for it to be created
correctly. For example, a web service needs the service mesh webhook
to be running when the service is created, for its pods to be
connected to the mesh.

This is not a desirable situation! But sometimes it is hard to
avoid. A mitigation is to arrange for the dependent to fail at
creation time if the dependency is unavailable (and be retried); at
least in that case, it won't end up in an incorrect state.

This is a hard dependency relation, but there can only be a best
effort mechanism for meeting it -- the dependency can transition out
of a ready state.

### The dependence mechanism

The basic idea of dependence-sensitive syncing is that a Sync object
declares its depedencies, and how they are met. Before the sync
controller applies configuration, it checks the requirements, and
defers the sync if they are not met.

#### Declaring dependence

 - there are two roles in play: the dependent, and the dependency
 - as a dependency you don't know that you'll be dependended upon, so
   not much point wiring things up there
 - as a dependent, you know what you'll need, but you don't have any
   say over how it's provided; so all you can do is say what you need
 - the glue is at "link" time, when you demonstrate that all the
   dependent's needs are met by something (in principle). For Starling
   that could mean when you define a Sync you say how the requirements
   are met (or it might just play out at runtime).
 - for a given configuration you should be able to tell whether all
   requirements are met

## Backward compatibility

 - a major concern is how to retrofit dependencies to existing chunks
   of configuration
   - some of the time, the dependencies are just for the happy path;
     so doing without is OK
   - otherwise, it can be gradual

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