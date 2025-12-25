-------------------------------- MODULE Environment --------------------------------

EXTENDS Integers, FiniteSets, Sequences, Naturals, TLC, Types


--------------------------------- MODULE User ---------------------------------
\* Actions performed by the user

LOCAL TasksToStart ==
    TaskID \ { t.id : t \in tasks }

LOCAL HaveReceivedInitExit ==
    /\ Len(SelectSeq(events, 
        LAMBDA e: e.type = taskExit /\ e.pid = 1 )) # 0

CreateInitTask ==
    /\ { t \in tasks: t.type = init } = { }
    /\ \E tsk \in TasksToStart :
        /\ AddTask(NewTask(tsk, init, unstarted))

CreateExecTask == 
    /\ { t \in tasks: t.type = init } # { }
    /\ ~HaveReceivedInitExit
    /\ \E tsk \in TasksToStart :
        /\ AddTask(NewTask(tsk, exec, unstarted))

User ==
    /\ UNCHANGED << exits, events, processes >>
    /\  \/ CreateInitTask
        \/ CreateExecTask

=============================================================================


------------------------------- MODULE Kernel -------------------------------
\* Actions performed by the kernel

(* RunProcess picks up a process in `processes' whose state is `starting' and
    runs it. The process is either successfully started - and has a pid assigned
    to it - or fails to start, and gets updated to the `failed' state.
    This allows the shim to finish handling this process's start *)
RunProcess ==
    /\ UNCHANGED exits
    /\ { p \in processes : p.type = init /\ (p.state = reaped \/ p.state = exited) } = { }
    /\ \E p \in { q \in processes: q.state = starting } :
        /\  LET pid == NewPid
            IN \* process starts successfully, gets a pid
               \/ UpdateProcesses(p :> [ id |-> p.id, state |-> running, pid |-> pid, type |-> p.type ])
               \* process fails to start
               \/ UpdateProcesses(p :> [ p EXCEPT !.state = failed ])

(* A process might fail to start. This is guaranteed to happen if 
    the process was added after the init process exited, or if it's added concurrently. *)
RejectProcessStart ==
    /\ UNCHANGED exits
    /\ { p \in processes : p.type = init /\ (p.state = reaped \/ p.state = exited) } # { }
    /\ \E p \in { q \in processes: q.state = starting } :
        \* start the process, and update the shim start with a pid
        /\ UpdateProcesses(p :> [ p EXCEPT !.state = failed ])

(* Reap processes that have exited. In a given namespace, the init pid will
    always be reaped last. *)
ReapProcesses ==
    /\ ExitedProcesses # { }
    /\  IF Processes(exited, exec) # { } THEN
            /\ \E processToExit \in Processes(exited, exec) : 
                    /\ AddExit(processToExit.pid)
                    /\ UpdateProcesses(processToExit :> [processToExit EXCEPT !.state = reaped])
        ELSE
            /\  \E processToExit \in ExitedProcesses :
                    /\ AddExit(processToExit.pid)
                    /\ UpdateProcesses(processToExit :> [processToExit EXCEPT !.state = reaped])

(* Tear down the pid namespace - all processes in this 
    namespace will exit, and processes starting concurrently will fail. *)
TeardownNamespace ==
    /\ UNCHANGED exits
    /\ Processes(exited, init) # { }
    /\ { p \in processes : p.state = running \/ p.state = starting } # { }
    /\ UpdateProcesses([p \in { q \in processes : q.state # reaped /\ q.state # failed } |-> 
                            IF p.state # starting THEN
                                [p EXCEPT !.state = exited] 
                            ELSE
                                [p EXCEPT !.state = failed]
                        ])
                            
(* If an init process has exited and there
    are running execs, the pid ns needs to be torn down. *)
ShouldTeardown ==
    /\ Processes(exited, init) # { }
    /\ { p \in processes : p.type = exec /\ (p.state = running \/ p.state = starting) } # { }

(* Tearing down a namespace needs to happen immediately after an init process exits.
    Otherwise, reap dead processes. *)
Kernel ==
    /\ UNCHANGED << tasks, events >>
    /\ IF ShouldTeardown THEN
            /\ TeardownNamespace
        ELSE
            \/ ReapProcesses
            \/ RunProcess
            \/ RejectProcessStart

=============================================================================


-------------------------------------------------------------------------------
\* The System

INSTANCE Kernel
INSTANCE User 

\* At any step in the system, a process might exit.
ProcessDies ==
    /\ UNCHANGED << exits, tasks, events >>
    /\ \E p \in RunningProcesses :
            \/ UpdateProcesses(p :> [p EXCEPT !.state = exited])

Environment ==
    \/ User
    \/ Kernel
    \/ ProcessDies


-------------------------------------------------------------------------------
\* Invariants

(* Invariant that events are never published out-of-order, i.e. a 
    TaskExit before a TaskStart (for the same pid). *)
ShimEventsNoStartBeforeExit ==
    \A i, j \in 1..Len(events): 
        i < j /\ events[i].pid = events[j].pid => 
            events[i].type = taskStart /\ events[j].type = taskExit

(* Invariant that if an init process has been reaped, no other processes are
    are running. *)
KernelProcessesInit ==
    Cardinality({ p \in processes : p.state = reaped /\ p.type = init }) = 1 =>
        \A q \in processes: 
            /\ q.state # running 

(* Invariant that, for every task in the `exited' state, there will be two
    (ordered) events - a `TaskStart' and `TaskExit' event. *)
ExitForEveryTask == 
    \A task \in {t \in tasks: t.state = exited}:
        \E p \in processes:
            /\ p.id = task.id
            /\ \E i, j \in 1..Len(events):
                /\ events[i].pid = p.pid /\ events[i].type = taskStart
                /\ events[j].pid = p.pid /\ events[j].type = taskExit

Invariants ==
    /\ ShimEventsNoStartBeforeExit
    /\ KernelProcessesInit
    /\ ExitForEveryTask

-------------------------------------------------------------------------------
\* Temporal Properties

(*  We're most interested in making assertions about the behaviour of the shim over time, that
    is, we want to verify statements such as "eventually, the shim will publish all exit events".

    In TLA+, these are so-called "Temporal Properties", and we define them using the following
    operators:
    
        `P~>Q' - meaning that every state which satisfies P "leads to" a state that satisfies Q 
            (this can be the same state, or a future state).

        `[]P' - meaning "always" P, i.e. for a behavior, every state will satisfy P.
        `<>P' - meaning "eventually" P, i.e. for a behavior, at least one state will satisfy P.
    
    We can use these last two together, to assert:

        `[]<>P' - meaning "always, eventually" P, i.e. for a behavior, every state either satisfies P
    or a future state will satisfy P, and

        `<>[]P' - meaning "eventually, always" P, i.e. for a behavior, eventually a state will satisfy P
    and every state thereafter that will satisfy P.  *)

(* Predicate that the init task is in the `failed' state. *)
InitFailed ==
    Cardinality({t \in tasks: t.state = failed /\ t.type = init }) # 0

(* Predicate that there is at least one event, and the last event published is
    a `TaskExit' event for the init pid. *)
InitExitPublishedLast ==
    /\ Len(events) > 0
    /\ events[Len(events)].pid = 1 /\ events[Len(events)].type = taskExit

InitExitAlwaysPublished ==
    \/ <>[]InitFailed               \* either the init task is eventually-always in the failed state, or
    \/ <>[]InitExitPublishedLast    \* the last event published is eventually-always a `TaskExit' for the init task

=============================================================================
