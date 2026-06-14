------------------------------ MODULE Types ------------------------------

EXTENDS Integers, FiniteSets, Naturals, Sequences, TLC

CONSTANT TaskID

-------------------------------------------------------------------------------
\* Processes

VARIABLE processes

unstarted == "unstarted"
starting == "starting"
running == "running"
exited == "exited"
failed == "failed"
reaped == "reaped"

(* Every state has a rank. It is only possible for a task to change
   state to a state with a higher rank (later in this sequence). *)
states == << unstarted, starting, running, exited, failed, reaped >>

(* Maps a state to its position in `order' (e.g. StateRank(new) = 1): *)
StateRank(s) == CHOOSE i \in DOMAIN states : states[i] = s

(* Convenient notation for comparing states: *)
s1 \prec s2   == StateRank(s1) < StateRank(s2)
s1 \preceq s2 == StateRank(s1) <= StateRank(s2)

(* A generic operator to get the range of a function (the set of values in a map): *)
Range(S) == { S[i] : i \in DOMAIN S }

(* The set of possible states ({unstarted, starting, ...}): *)
ProcessState == Range(states)

init == "init"
exec == "exec"

ProcessTypes == {
    init,
    exec
} 

Process == [
    id : TaskID,
    pid : Int,
    state : ProcessState,
    type : ProcessTypes
]

RunningProcesses == { p \in processes : p.state = running }
ExitedProcesses == { p \in processes : p.state = exited }

pids == {p.pid : p \in processes}

Processes(state, type) == { p \in processes : p.state = state /\ p.type = type }

NewPid ==
    CHOOSE n \in 1..100 : n \notin pids

NewProcess(id, type) == 
    [   id |-> id,
        pid |-> -1,
        state |-> starting,
        type |-> type ]

AddProcess(p) ==
    /\ Assert(p.id \notin { o.id : o \in processes}, "process with this pid already exists")
    /\ Assert(p \in Process, "does not satisfy the Process type invariant")
    /\ processes' = processes \cup { p }

(* Update `processes' by performing each update in `f', which is a function
   mapping old processes to new ones. *)
UpdateProcesses(f) ==
    /\ Assert(\A p \in DOMAIN f : p \in processes, "An old process does not exist!")
    /\ Assert(\A p \in DOMAIN f :
                LET p2 == f[p]
                IN                                  \* The updated version of `p' must have
                /\ p.id      = p2.id,               \* the same process ID
            "An update changes a task's identity!")
    /\ Assert(\A p \in DOMAIN f :
                LET p2 == f[p]
                IN                                  \* The updated date version of `p' must represent
                /\ p.state \preceq p2.state,        \* a valid state transition
            "An update does an invalid process state transition!")
  \* Remove all the old processes and add the new ones:
  /\ processes' = (processes \ DOMAIN f) \union Range(f)


-------------------------------------------------------------------------------
\* Tasks

VARIABLE tasks

Task == [
    id: TaskID,
    type: ProcessTypes,
    state: ProcessState
]

NewTask(id, type, state) == 
    [ 
        id |-> id,
        type |-> type,
        state |-> state
    ]

AddTask(t) ==
    /\ Assert(t.id \notin { r.id : r \in tasks}, "task with this id already exists")
    /\ Assert(t \in Task, "does not satisfy the Task type invariant")
    /\ tasks' = tasks \cup { t }

(* Update `tasks' by performing each update in `f', which is a function
   mapping old tasks to new ones. *)
UpdateTasks(f) ==
    /\ Assert(\A t \in DOMAIN f : t \in tasks, "task to update does not exist")
    /\ Assert(\A t \in Range(f) : t \in Task, "an updated task does not satisfy the Task type invariant")
    /\ Assert(\A t \in DOMAIN f :
                LET t2 == f[t]
                IN                          \* The updated version of `t' must have
                /\ t.id      = t2.id        \* the same task ID, and
                /\ t.type = t2.type,        \* the same task type.
            "an update changes a task's identity")
  \* Remove all the old tasks and add the new ones:
  /\ tasks' = (tasks \ DOMAIN f) \union Range(f)


-------------------------------------------------------------------------------
\* Exits

VARIABLE exits

Exit == Nat

AddExit(exit) ==
    /\ Assert(exit \in Exit, "does not satisfy Exit type invariant")
    /\ exits' = Append(exits, exit)

NextExit(f(_)) ==
    /\ Len(exits) > 0
    /\ LET exit == Head(exits)
        IN 
            /\ f(exit) 
            /\ exits' = Tail(exits)


-------------------------------------------------------------------------------
\* Events

(* TODO:
        - TaskCreate
        - TaskExecStarted
        - TaskExecAdded *)

VARIABLE events

taskStart == "taskStart"
taskExit == "taskExit"

eventTypes == << taskStart, taskExit >>

(* Maps a state to its position in `order` (e.g. StateRank(new) = 1): *)
TypeRank(s) == CHOOSE i \in DOMAIN eventTypes : eventTypes[i] = s

(* The set of possible event types ({taskStart, taskExit, ...}): *)
EventType == Range(eventTypes)

Event == [
    pid : Nat,
    type : EventType
]

NewEvent(pid, type) == [
    pid |-> pid,
    type |-> type
]

PublishEvents(es) ==
  /\ Assert(\A i \in 1..Len(es) : es[i] \in Event, "An event is not an event!")
  /\ events' = events \o es

-------------------------------------------------------------------------------
\* Invariants

TypeOK ==
    /\ tasks \in SUBSET Task
    /\ processes \in SUBSET Process
    /\ \A i \in 1..Len(events):
        events[i] \in Event
    /\ \A i \in 1..Len(exits):
        exits[i] \in Exit

=============================================================================
