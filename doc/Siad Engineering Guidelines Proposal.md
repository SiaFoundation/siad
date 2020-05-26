### TODO
 - Fill in all code examples
  
  
# Siad Engineering Handbook 
Siad is an ambitious engineering project aimed at giving individuals independent
control over their data, at speeds, costs, and reliabilities that exceed the
traditional paradigms for data control. It cannot be said enough that the
engineering behind siad is challenging, often in surprising and unexpected ways.
A key focus for siad is an emphasis on navigating the engineering challenges and
complexities that are inherent to the task of decentralized cloud storage.

Siad is a team effort, built by many engineers working together on problems that
frequently overlap and interact with each other. Often, these interactions can
span years of code, with code written many years ago having interactions with
code being written today. It is a goal that even entry level engineers should be
able to effectively work on and contribute to the siad codebase. A key focus for
the codebase is an emphasis on writing code that scales across a large team
working over large periods of time.

Sia is a mission. Sia is trying to enable the individual to have more control
over their digital lives. Sia is trying to replace the conventional cloud
platforms of today. This means providing superior features at a competitive
price. A key focus for siad is shipping timely, effective, and practical
solutions to the world.

Stated more concisely, siad engineering follows three major principles. The
first is that complexity needs to be managed aggressively. The second is that
code needs to be written for a team that is composed of engineers that span
a wide range of backgrounds and experience levels. And the third is that the
team needs to continually ship code that materially improves upon existing code.

These three principles are used as the guiding stars for everything else that
appears in this document.

## General Advice
Present architecture over implementation. Someone reading a major startup
function, background loop, exported routine, or other architectural element
should be able to get a general sense of how everything works without needing to
scroll through the exact details. Implementation details should be moved to
helper methods. Helper methods should have names that make reading the
implementation details or docstrings unnecessary to understand the intent and
strategy of the code.

Don’t be clever. Code needs to be readable, and ideally code needs to be
obviously correct. Engineers with less experience should feel comfortable
looking at any particular piece of code and understanding how it works.
Sometimes, this means that the code needs to be more verbose or have worse
performance properties. Where performance cannot be sacrificed for readability,
large comments should be substituted explaining exactly what is happening.

Verbose is better than brief if being verbose makes the code more clear. As you
design code, keep in mind what context other programmers are expected to have as
they read your code. Code should have as little context as possible, another
programmer should have to read as little code as possible, reference as few
structs as possible, look up as little documentation as possible to understand
a function. Shorthand names like ‘r’ for ‘renter’ are acceptable inside of
a function where ‘r’ is only using in that one scope, but are less acceptable in
structs because structs will appear in multiple scopes (minimally 2, the scope
of the struct definition and the function where the struct is used).

Uniformity is better than perfection. It’s jarring to change styles in the
middle of a coding session, ideally everything always uses the same style and
pattern. Even if a pattern is not perfect, it’s often better to keep using the
same pattern than to introduce a new pattern. This minimizes the mental effort
of other people using the code.

Though it was linked earlier, this article is worth linking again. It’s a great
resource with lots of relevant advice: Practical Go: Real World Advice for
Maintaining Go Programs 

## Programming Language Choice
Siad is a pure-Go project. Sia committed to being built in Go in 2014, and this
often have attractive features, however golang is a good enough language for
siad, and the switching costs are very high. A change to a different language is
almost certainly not going to happen.

Go is a language that was built by engineers, for engineers. The experience and
practicality of the golang team is visible in almost every design decision made
within the language. While often restrictive, building within the confines of
the original intentions of the language provide a flexible, powerful, robust,
and yet simple toolset that is capable of meeting all of the needs of siad.

Siad code is strongly encouraged to stay within the confines of idiomatic Go.
Idiomatic Go is well thought through and for the most part does an excellent job
of facilitating the key principles that Sia pursues. Especially when it comes to
working as a team, one engineer building something that strays from idiomatic go
increases overhead and complexity for all other engineers that have to work with
that code.

This is an excellent write-up for understanding more about Go and best practices
within the language: Practical Go: Real World Advice for Maintaining Go Programs

If you are completely new to the language, we also recommend the following
links: 

A Tour of Go How to Write Go Code Effective Go

## Ecosystem Constraints
Sia is a platform with an ecosystem built on top. This ecosystem is composed of
large amounts of software that depend on the various APIs that siad has released
over the years. In the interest of preserving the ecosystem and enabling apps to
enjoy a long shelf life, siad enforces strict compatibility with all APIs that
are released for public use.

For similar reasons, siad cannot have feature or performance regressions.
Especially for topics like minimum file size and maximum speed or latency,
performance regressions can be equivalent to broken APIs.

The performance constraint operates both forward and backward. Once a certain
benchmark is obtained, all future features must protect and maintain that
benchmark. This means that developers need to be cautious with shortcuts and
temporary fixes. If a temporary fix enables certain speeds but only because of
a temporary structure or because of a hack that cannot be maintained long term,
that fix cannot be merged into production.

These constraints are one of the primary reasons siad refuses to lean on any
centralized services. Those services can enable features or performances that
may not be replicable if siad were to move away from these services to be fully
decentralized. To prevent the project from getting locked into a centralized
crutch, the project refuses to depend on any centralized techniques in the first
place.

## Daemon Requirements
Siad has several design constraints to keep the software running smoothly and
safely. The simplest requirement is that the daemon codebase is not allowed to
have bare panics unless not panicking would directly risk the user’s funds or
the user’s data. Instead of panicking, build.Critical should be called, which in
production will print out a scary message along with a link to the Gitlab issue
tracker.

The second requirement is that the daemon must start up quickly, with features
and API endpoints being made available to the user as soon as they are ready
(even one at a time) rather than waiting until all modules have fully started to
present an API to the user. For the most part, this means doing slower startup
sequences in the background, and having functions which depend on these slow
sequences return errors until the required startup is completed.

The third and most burdensome daemon wide requirement is that a clean shutdown
must be instant, and yet a clean shutdown must also exit gracefully. Threads
must have time to close out, sleeps must be interrupted, open network
connections must be canceled or otherwise instantly begin their termination
procedure. The primary tool for facilitating clean and quick shutdown is
a ThreadGroup.

Aside from the renter and host, the daemon should be kept under 250 MiB of
memory usage. This is generally not a major constraint but does mean that most
modules are not allowed to have large data structures. The renter aims to
consume less than 2 GiB total (a target that will shrink over time), and the
host aims to consume less than 1 GiB per 4 TiB of data storage. A key thing to
remember is that Go is garbage collected, which means generally the daemon will
consume 2x memory - an object that is 1 MiB in size will consume 2 MiB of
operating system memory.

## Concurrency Patterns and Requirements
At any given time, siad will have up to 1000 goroutines all accessing various
objects concurrently, with many of the goroutines interacting independently with
multiple of siad’s stateful objects.  Concurrency, when used correctly, can help
to substantially simplify a codebase.  Many background threads can operate at
once, each fully oblivious to the others.  Unfortunately, Go does not guarantee
that concurrent operations are safe at compile time. To overcome this limitation
of Go, siad enforces additional requirements to ensure that concurrent processes
are thread safe.

These requirements make building individual components of siad more challenging,
but make connecting diverse components together easier, safer, and more
scalable. The general idea is to push the complexity as far down the stack as
possible, where the total number of interacting parts are small, that way
complexity is as little as possible higher up the stack, where the total number
of interacting parts are large. The goal is to have the final system - siad
- have as few complex interactions as possible.

One major requirement is that all exported functions in a siad library and all
exported methods of a siad object must always be thread safe. It must always be
okay for exported functions of an object or package to be called multiple times
from multiple threads in any arbitrary order. For stateless libraries, this
requirement is trivial to enforce. For more stateful modules, this can require
some extra care and handling. Enforcing this requirement gives developers the
freedom to interact freely with other packages and not need to worry about what
other threads in Sia are doing.

Another key concurrency requirement is that no external package or method can
ever be called or used while the caller is holding a mutex or other blocking
primitive. This prevents deadlock scenarios where one multiple objects are stuck
waiting for each other to release a block before unblocking themselves. The
complexity of safely navigating multiple objects that block while making
external calls has consistently proven to be beyond what humans can safely code.

## Struct Field Naming Conventions
Siad adds additional safety to its concurrent processes by enforcing explicit
naming conventions on struct fields. The purpose of these naming conventions is
to ensure that the developer always has context when programming around how each
field and method needs to be used. Currently, these requirements are only
enforced by code review, however they are extremely helpful and one day we hope
to enforce them via a linter or via some other compile-time tool.

By default, every field is controlled by a mutex. If the struct itself has
a mutex, the field is controlled by the mutex in the struct. If the struct has
no mutex, the field is controlled by the nearest ancestor struct that does have
a mutex.

Each struct is only allowed to have one mutex. If a struct has multiple locking
domains, those domains each need to be split into their own structs. This
requirement is in place to minimize confusion and keep code consistent
throughout the codebase.

Example one: <code example struct with mutex>

Example two: <code example child struct where parent struct has mutex>

The static prefix indicates that a field is never written to after
initialization. When the struct is initialized, before it is made available to
any thread besides the creator thread, the field must be set. And once set, the
field must never be altered. This allows any number of threads to access the
field simultaneously with no need for a mutex. As a special note, maps can be
static, the Go runtime only updates a map’s state during a write action. When
a struct has a pointer to another struct in it, that pointer is generally
static, because while the underlying struct may be modified, the pointer is
usually not ever modified and the field itself is the pointer, not the
underlying struct.

Example three: <code example struct with static field>

Example four: <code example struct with static other struct in it>

The atomic prefix indicates that the field is always accessed using the atomic
package from the standard library. All reads and writes to the field must be
done using the atomic package. The atomic prefix also implies that the field is
independent of the other fields in the struct, two atomic fields cannot be
updated simultaneously, and therefore the programmer cannot depend on any atomic
field to have a reliable or consistent relationship with any other field. Atomic
fields have another requirement - they must be placed at the top of the struct,
and the 64 bit fields must be placed first. This is because on 32 bit systems,
atomic fields need to be aligned in a special way, and the best way to ensure
this alignment happens is by putting the all atomic fields at the top of
a struct, and all 64 bit fields above all of the other atomic fields.

Example five: <code example of a struct with atomic fields>

The extern prefix is a special, rarely used, prefix that indicates a field is
controlled by some other mutex than the assumed mutex. A developer will have to
reference the docstring of the struct to understand how the field is meant to be
used. There are places in the code where the ‘owned’ prefix appears, which is
now deprecated, ‘extern’ should be used instead. ‘Owned’ is a subset of extern
where the field is restricted to being accessed by a particular background
thread, meaning that thread does not need to worry about using a mutex when
accessing and modifying the field.

## Method Naming Conventions
A method is a function on a struct.  

Example one: <code example of a method declaration>

If the method is exported, the method is required to be thread safe. The
developer should assume that this method can be called any number of times with
any amount of currency in any number of ways, and the method must be thread safe
under all conditions.

Example two: <code example of an exported method, containing locking>

If the method is not exported and contains no other prefix, the method is
required to be called while holding a lock on the parent object. The method
implementation must assume that a lock is being held, and therefore cannot grab
a lock itself, but also is able to interact freely with the non-prefixed fields
of the parent object.

Example three: <code example of a no-prefix method, interacting with fields>

The static prefix indicates that the method exclusively interacts with static
fields of the parent object, and therefore no lock needs to be held before
calling the method. The method must not grab a lock at any point, which means
the method can also be called while the caller is holding a lock. The static
method is also allowed to interact with atomic fields of the object.

Example four: <code example calling a static method with and without a mutex,
method interacts with static and atomic fields of the object>

The managed prefix indicates that the method internally grabs and releases
a lock. That means the caller must not be holding a lock when it calls the
method. The method internally can interact with all fields of the struct,
however needs to grab a lock before interacting with any of the protected fields
of the struct.

The threaded prefix indicates that the method needs to be called in a goroutine,
usually because the method is a background loop that runs indefinitely, or
otherwise has some operation that blocks for a very long time. Aside from the
goroutine requirement, the threaded prefix implies all of the same things that
‘managed’ implies. Note that a method does not need to be prefixed with
‘threaded’ to be called in a goroutine.

## Error Management and Propagation
All errors should be checked and handled, always. If an error is being returned
to a higher level function, that error should be wrapped with
`errors.AddContext` from the NebulousLabs/errors package.  Context is
a requirement because, prior to the requirement, it was not uncommon for some
complex action like “UploadFile” to return the error “EOF”. Now that we have
context, we get an error like “could not open source file: EOF” or “remote host
returned error: EOF”, either of which is substantially more useful when
debugging.

If an error is expected to be interpreted by another piece of code, that error
needs to be named as an exported constant. Error checking should never use the
“strings.Contains()” pattern unless we are checking an error on a dependency
that doesn’t follow the siad coding guidelines.

## Docstrings, Comments, TODOs, NOTEs
Every constant, variable, function, and struct in the siad codebase must have
a docstring that explains how to use it.  Comments should be broken into
paragraphs (where multiple paragraphs are necessary) and paragraphs should be
spaced with a break line between paragraphs.  Comments should be wrapped to 80
characters. In vim, the command ‘gq’ can be used after highlighting a block of
text in visual mode to wrap a comment.

Example: <struct with a multi-paragraph comment>

Code should be well commented. Each code block should have a concise, easy to
read description of what that code block does. This is not always required of
every code block, but a developer who can only read comments and not code should
still feel like they get the general idea of what the code is supposed to be
doing.

All code that is tricky, fancy, requires extra context, or is just generally
involved or confusing should have a comment that breaks the code down into
simpler terms and highlights all of the fancy techniques. This is especially
true for performance code, where breaking the code down into more lines and more
readable lines may not be acceptable for performance reasons.

Performance code aside, code that needs heavy commenting to be made easily
accessible is probably poorly written code. Favor simplifying code at the cost
of performance or line count over having lots of comments. Simpler code is
better code.

A TODO is a note from the developer about something that needs to be done. A lot
of the older code in Sia has a large number of TODOs in it. Today, TODOs are
encouraged while building a new piece of code to keep track of what else should
be done, however merging TODOs is highly discouraged. Either the code should be
completed, or the TODO should be removed an a gitlab issue should be created
instead. Where possible, existing TODOs should be completed, removed, or turned
into issues.

A NOTE is a note from the developer. Sometimes, a NOTE can be used like a less
urgent TODO, perhaps explaining that a piece of code has poor performance
properties, and suggesting a structure that has better performance properties.
These types of notes may never be implemented, for example if the code in
question never causes bottlenecks in production then there will never be a need
to switch to the faster suggestion. NOTEs can also just be extra context,
history, or something else that the developer believes is important for the
reader to know even if it is not directly related to the code.

## Other Code Conventions
Sorting variables can be a challenge. Generally, we like to eliminate as much
mental burden as possible. When in doubt, alphabetize!  Sorting your variable
names alphabetically is not strictly required, but it is a good cop-out when you
aren’t sure how else to order things. Generally, you shouldn’t be spending a ton
of time on trivial things like how your variables are ordered, and the
alphabetization strategy is a good way to feel productive without having to
think too hard or waste too much time.

The term ‘NDF’ stands for ‘non-deterministic failure’, and refers to any test
which sometimes passes and sometimes fails. NDFs can slow down development
process and harm CI, and can also hide actual bugs which may affect users in
production (though it’s usually the former). Unfortunately, it’s impractical to
have a strict policy around NDFs, this would slow down development too much. If
a test is found to have an NDF, the test writer should be notified, and they are
expected to explore / solve the NDF within a few days. If the NDF ends up being
trickier, it does not need to be resolved immediately. NDFs are technical debt,
and developers are urged to resolve NDFs as quickly as reasonable.

A frequent anti-pattern seen in the Sia codebase is entangled state. Two objects
or pieces of state are said to be entangled if they each contain some field or
value that is supposed to be inherently related between the two objects. At one
point in time, the Sia allowance resulted in a large amount of entangled state
- the allowance was stored separately in multiple objects throughout the renter
  code, and every time the allowance was updated or changed, all of those
objects needed to be notified / updated as well. A non-entangled alternative
would be to have each object call out to the allowance subsystem and ask for the
allowance each time the allowance is needed for a computation.

## Codebase Filesystem Layout
The gitlab repo uses the following folder layout:

/cmd 
/docs 
/internal 
/pkg 
/test 
/vendor

/cmd has all of the Sia core binaries. These binaries aim to have as little code
as possible, drawing most of their code from libraries in /pkg and /internal.

/docs contains important documentation related to Sia.

/internal contains the majority of the Sia logic. /internal is composed of
libraries that have unstable APIs. This includes most of the modules and many of
the high level types.

/pkg contains libraries that have stable APIs. Sia maintains strict
compatibility on the APIs found in the /pkg library, meaning that external
developers can expect to be able to import the libraries for their code without
fearing that the Sia devs may break the code in the future.

/test contains all of the integration testing for the Sia code. Most of these
tests spin up a fleet of nodes that each have different roles (such as renter,
host, miner, etc.) and uses these nodes to test various edge cases and user
scenarios.

/vendor contains all packages and libraries that Sia uses from third parties
whose APIs are either unstable or whose upstream code is otherwise unusable on
the master branch.

## Siad Modules
A siad module is a single stateful object that exports some class of methods.
Modules are usually fairly complex, and often have submodules.  A submodule is
just a module that doesn’t appear in the top level modules package, but
otherwise follows all other module standards.

Modules are generally instantiated by using the ‘New’ function, and generally
are expected to last the lifetime of the program. Modules can be closed with the
‘Close’ method. Siad should not exit until all modules have been given a chance
to close gracefully.

Most modules have their own logger, which gets created using the
internal/persist package. Most modules also support dependency injection, which
is explained further on in the document.

Other than being able to call exported methods on each other, modules do not
share state and do not share persistence domains. Calling Save() on one module
will not impact the persistence of other modules, and modules should be
programmed to not require any consistency with other modules.
‘ProcessConsensusChange’ is a good example of managing cross-domain consistency.
At startup, all consensus subscribers will tell the consensus module which
update they have received most recently, eliminating the need to coordinate
persistence across modules.

Siad modules often share types, and this can cause challenges with import loops.
To categorically avoid import loops, we have a top level modules package that
all modules are allowed to import. Types which are shared across modules will
generally go in this package.

When working within a module, a developer generally will not need to know more
than what is in the README. Developers should not need to read the module code
or get deep into the internals of a module before they understand enough to make
productive changes.

<Example of a blank / dumb Sia module that contains an exported method, a New
function, a Close function, a mutex, some subsystems, a logger, and dependency
injection>

## Siad Subsystems
When developers work on pieces of code within siad, they will usually be working
on a particular subsystem. A subsystem can be viewed as a highly self-dependent
piece of code. A developer working on a subsystem should be expected to read all
of the code within the subsystem before making any changes to the subsystem. As
such, it is important to keep the subsystem small and simple, to minimize the
onboarding burden of new developers into a subsystem.

A file called subsystemname.go should contain all of the constants, types, and
callable methods of the subsystem. If the subsystem is small, all of the logic
should be in this file as well. If the subsystem is larger (>200 loc), pieces of
logic can be split out into other files that must have their names prefixed by
subsystemname.

Subsystems are generally stateful, and when they are stateful they are expected
to have their own mutex and methods. A subsystem should not be defining methods
on the parent object, only on itself. Most methods of a subsystem are expected
to be internal to the subsystem - outside subsystems and other outside logic in
the package are not allowed to call or interact with most fields or methods of
a subsystem. Subsystems will generally have a reference to their parent object,
so that they may use the dependencies, the logger, etc. of the parent.

Methods that are meant to be called by external code must appear in
subsystemname.go, and must have the method prefix ‘call’. Similar to package
conventions, ‘call’ implies that the method is thread-safe to call from other
subsystems, will not call out to other subsystems while holding a mutex, and
should not be called while a mutex is being held.

Every subsystem needs to be documented in the module README. The overall
intention and architecture of the subsystem should be documented. Also required
to be documented are all interactions with other subsystems that use the ‘call’
methods. Both outgoing and incoming interactions need to be documented.

<Example of a subsystem README>

<Example of a simpler subsystem, containing its own mutex, a callout function,
an internal function, and a parent object>

## ACID
Siad is an ACID project, which means that all data saved to disk must remain
consistent and durable. (atomic and idempotent, the other letters in ACID, are
more of strategies to achieve consistency and durability, rather than desirable
properties in and of themselves).

For simpler persistence structures, it’s usually enough to use the persist
package’s SaveJSON and LoadJSON functions. These functions have been thoroughly
tested and will achieve consistency and durability. It is important to note
however that consistency can only be achieved within one object at a time. If
multiple objects are calling SaveJSON separately, the persist package cannot
guarantee that these objects will remain consistent with each other. Most
modules have only one object that gets persisted, so this is usually not an
issue.

SaveJSON and LoadJSON are slow! There is a large performance cost to these
functions, and so they should only be used infrequently and outside of contexts
where performance is important. When performance is important,
NebulousLabs/writeaheadlog is typically the tool of choice.

ACID is very difficult. When doing anything outside of the typical complexity,
always involve a senior engineer. Typically, both David and Chris should be
involved in signing off on complex ACID code. It’s very easy to miss something
subtle and write code that is not actually ACID. Don’t write ACID code alone!

## Backwards Compatibility of Persistence
Persisted files are always versioned in siad. The version number given to
a persisted file in siad is the last version where compatibility was broken. We
only consider compatibility to be broken when the previous code would be unable
to successfully read and load the file.

Adding new fields to objects that use SaveJSON will usually not break
compatibility. The startup code can check if the new field is missing, and then
instantiate the new field with a default value. Removing fields is also often
not problematic, if the field truly doesn’t have any meaning anymore.

Sometimes however, a persistence change will require a migration. The old
persist will need to be loaded, parsed, and then transformed into a new persist
that is then saved. That process is considered to be compatibility breaking, and
requires a version update.

Migration code always has the name persistObjVxxxToPersistObjVyyy. At startup,
the version of the persisted data will be checked, and if the version is ‘xxx’,
the migration code will be used to upgrade the persist to version ‘yyy’. There
may be another function persistObjVyyyToPersistObjVzzz, in which case the
migration will be chained.

<Example: link to a place where a migration happens in the siad codebase>

We try to ensure that any old version of siad can upgrade to any new version of
siad without the user needing to install every incremental version. Generally,
this strategy has been sufficient to achieve that.

## API
### Design
TODO
 - RESTful practices
 - Naming conventions
 - Unit conventions
 - General Design conventions

### Backwards Compatibility
The API must always maintain full backwards compatibility. Once an app has been
built on top of a version of siad, that app should work as the developer
intended for all future versions of siad. A user should be able to upgrade siad
without any of the apps being impacted and without any developers being required
to update their app code.

Backwards compatibility is only maintained with endpoints that are documented
(or have been previously documented) at https://sia.tech/docs - an endpoint may
exist within siad that has not been documented, this endpoint should be
considered unstable and not a part of the compatibility promise.

If an endpoint is found to be unfavorable or problematic, the endpoint can be
deprecated. This means that it is marked as deprecated, and an alternative is
suggested in the api docs for one minor release. After that, the deprecated
endpoint will be removed from the api docs. Support will still be provided for
this endpoint, and testing will still be present to ensure that the endpoint
behaves as previously documented, however new developers will not be able to
find the endpoint without intentionally looking for deprecated endpoints.

All API endpoints should be explicitly tested by a siatest, to verify that the
endpoint has the promised behavior. These tests should never be removed or
broken, as they ensure that the compatibility promise on API endpoints is
actually enforced. Third party app developers are encouraged to write their own
tests in siad for the API endpoints they use, such tests will help the siad team
ensure that compatibility is never accidentally broken.

## Unit Testing, Integration Testing, VLong Testing
Unit testing goes into the same package as the code that is being tested. Unit
tests are intended to take less than five seconds per test, and generally should
not be spinning up more than one sia node. Where possible, unit tests shouldn’t
have any state at all.

All integration testing should be done in the test/siatest package. Modules are
fairly complex, and often attempt to self-repair or have other behaviors which
can make testing annoying and non-deterministic. The siatest package has a lot
of tooling to manage this, and overall results in tests that are much easier to
maintain over a long period of time. Integration testing performed outside of
the siatest package has consistently proved to be a massive maintenance burden,
which is why the siatest package was created. All complex testing should be done
in the siatest package.

<links to examples of good siatest tests>

A test that is stable, never flakes, never fails unexpectedly, and rarely breaks
due to code changes can be moved to VLong. VLong tests are run by the continuous
integration system every night, but are not run every merge. Developers are not
expected to run VLong tests locally, and generally speaking it’s not expected
that a VLong test will start failing due to some code being changed - VLong
tests are typically quite stable even as the underlying implementation changes
significantly.

<Example of a vlong test>

## Dependency Injection and Code Disrupts
Sometimes for testing it is necessary to perform dependency injection. For
example, it is common in siad to have a dependency that simulates a faulty disk,
returning errors or incomplete writes randomly when os.Open is called.

Siad modules will have a ‘dependencies’ interface which implements a bunch of
external dependencies. For example, osOpenFile() would be a method of the
dependency interface that simulates os.OpenFile(). A production dependency
object is created which implements the production dependencies.

When creating a new dependency to inject into the module for testing, that new
dependency should embed the production dependencies. Embedding the production
dependencies allows the programmer to use the production dependencies without
needing to redefine the whole dependency interface. The new object can overload
the required methods of the injected dependencies, creating minimal burden on
the programmer while allowing fine grained control over dependency behavior.

<Example: write a dependency object with some methods, then write an injection
object with one of the methods overloaded>

Sometimes it is necessary to manually adjust siad’s internal logic directly. For
example, if a test is trying to verify that a certain change resulted in
a failed upload, but there is background code that keeps repairing the failed
upload, it may be necessary to perform a “disrupt” which prevents the repair
code from running.

The disrupt pattern is very simple. In the main code, a branch calls “if
disrupt(“disruptRepairLoop”) { return }”. The production disrupt dependency will
always return false for all inputs. An injected disrupt dependency can return
true for specific strings, targeting specific branches to execute.

<Code example of a disrupt>

## Resource Hygiene
Some resources imply a follow-up action to be used safely.  For example, calling
mutex.Lock() implies that somewhere there needs to be a mutex.Unlock().
Similarly, persist.NewLogger() implies that somewhere, the logger is going to
need to be closed.

Resources that imply a release should define that release immediately. A lock
needs to either be unlocked in the same codeblock, or needs to unlock using
a defer. Resources like a new logger are generally scheduled to be closed by
a threadgroup.

<lock; defer unlock example>

<lock unlock in one codeblock example>

<lock unlock not in one codeblock example - an example of what is bad>

<newlogger; tg.OnStop example>

## Afterwards: 
[c]Most websites have a developers page. We could split it out 1. Users 2.
Developers 
