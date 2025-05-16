---------------------------- MODULE Shim ----------------------------------

EXTENDS FiniteSets, Naturals, Sequences, TLC, Types


-------------------------------------------------------------------------------
\* Utils for managing the shim's state

VARIABLE shimState

AddRunning(shim, pid) == [ shim EXCEPT 
        !.running = shim.running \cup { pid }
    ]

RemoveRunning(shim, pid) == [ shim EXCEPT 
        !.running = shim.running \ { pid }
    ]

AddSubscriber(shim, id) == [ shim EXCEPT 
        !.exitSubscribers = shim.exitSubscribers \cup { [ id |-> id, exits |-> {} ] } 
    ]

RemoveSubscriber(shim, id) == [ shim EXCEPT 
        !.exitSubscribers = { s \in shim.exitSubscribers : s.id # id }
    ]

BroadcastExit(shim, exitPid) == [ shim EXCEPT 
        !.exitSubscribers = { [ s EXCEPT !.exits = s.exits \cup { exitPid }  ] : s \in shim.exitSubscribers }
    ]

AddStartingProcess(shim, id) == [ shim EXCEPT 
        !.startingProcesses = shim.startingProcesses \cup { id }
    ]

RemoveStartingProcess(shim, id) == [ shim EXCEPT
        !.startingProcesses = { t \in shim.startingProcesses : t # id }
    ]

StartNewTask(shim, taskId) ==
    AddSubscriber(AddStartingProcess(shim, taskId), taskId)

IncRunningExecs(shim) == [ shim EXCEPT 
        !.runningExecs = shim.runningExecs+1
    ]

DecRunningExecs(shim) == [ shim EXCEPT 
        !.runningExecs = shim.runningExecs-1
    ]
-------------------------------------------------------------------------------


------------------------------- MODULE RuncShim -------------------------------
\* Actions performed by the Shim

InitStart ==
    /\ UNCHANGED << events, exits >>
    /\ \E task \in { t \in tasks : t.type = init /\ t.state = unstarted } :
        /\ UpdateTasks(task :> [task EXCEPT !.state = starting])
        /\ AddProcess(NewProcess(task.id, init))
        /\ shimState' = StartNewTask(shimState, task.id)

(* Start a new exec. Concurrent starts are modelled in this specification by adding
    starting tasks to `startingProcesses', and separately picking up `starting' 
    threads -after they have a pid/have failed to start- and processing them in
    `handleStarted',with no ordering guarantee. 
    This matches the behaviour in the shim, since there is no lock enforcing ordered
    processing of concurrent starts. *)
ExecStart ==
    /\ UNCHANGED << events, exits >>
    /\ { t \in tasks : t.type = init /\ t.state = running } # { }
    /\ shimState.containerInitExit = {}
    /\ \E tsk \in { t \in tasks : t.state = unstarted } :
        /\ UpdateTasks(tsk :> [tsk EXCEPT !.state = starting])
        /\ AddProcess(NewProcess(tsk.id, exec))
        /\ shimState' = StartNewTask(IncRunningExecs(shimState), tsk.id)

(* After a process has started, it either:
    - failed to start, in which case we update `tasks' 
    - succeeded, in which case the shim publishes a `TaskStart' event, and checks
        the `exitSubscriber' for exits: 
        - if there is an exit for it's pid, we publish a `TaskExit' event, and, don't add it to `running'.
        - if there isn't, we add it to `running'.
    In all cases, we remove it's subscriber from `exitSubscribers' *)
HandleStarted(startingProcess) == 
    /\ UNCHANGED << processes, exits >>
    /\  LET process == CHOOSE p \in processes : p.id = startingProcess
            task == CHOOSE t \in tasks : t.id = startingProcess
        IN
            IF task.type = init THEN
                /\ \E subscriber \in { s \in shimState.exitSubscribers : s.id = process.id }:
                    /\  IF process.state = failed
                        THEN \* process failed to start
                            /\ UNCHANGED events
                            /\ shimState' = RemoveStartingProcess(
                                RemoveSubscriber(shimState, process.id), process.id)
                            /\ UpdateTasks(task :> [task EXCEPT !.state = failed])
                        ELSE \* start was successful and we have a pid
                            /\  IF Cardinality({ exit \in subscriber.exits : exit = process.pid }) > 0
                                THEN \* we received an exit in the meantime for this pid
                                    /\ PublishEvents(<<NewEvent(process.pid, taskStart), NewEvent(process.pid, taskExit)>>)
                                    /\ shimState' = RemoveStartingProcess(
                                        RemoveSubscriber(shimState, process.id), process.id)
                                    /\ UpdateTasks(task :> [task EXCEPT !.state = exited ])
                                ELSE \* no exit received, add to running
                                    /\ PublishEvents(<<NewEvent(process.pid, taskStart)>>)
                                    /\ shimState' = RemoveStartingProcess(
                                            RemoveSubscriber(AddRunning(shimState, process.pid), process.id), process.id)
                                    /\ UpdateTasks(task :> [task EXCEPT !.state = running ])
            ELSE 
                /\ \E subscriber \in { s \in shimState.exitSubscribers : s.id = process.id }:
                    /\  IF process.state = failed
                        THEN \* process failed to start
                            /\ UNCHANGED events
                            /\ shimState' = DecRunningExecs(RemoveStartingProcess(
                                RemoveSubscriber(shimState, process.id), process.id))
                            /\ UpdateTasks(task :> [task EXCEPT !.state = failed])
                        ELSE \* start was successful and we have a pid
                            /\  IF Cardinality({ exit \in subscriber.exits : exit = process.pid }) > 0
                                THEN \* we received an exit in the meantime for this pid
                                    /\ PublishEvents(<<NewEvent(process.pid, taskStart), NewEvent(process.pid, taskExit)>>)
                                    /\ shimState' = DecRunningExecs(
                                        RemoveStartingProcess(RemoveSubscriber(shimState, process.id), process.id))
                                    /\ UpdateTasks(task :> [task EXCEPT !.state = exited ])
                                ELSE \* no exit received, add to running
                                    /\ PublishEvents(<<NewEvent(process.pid, taskStart)>>)
                                    /\ shimState' = RemoveStartingProcess(
                                            RemoveSubscriber(AddRunning(shimState, process.pid), process.id), process.id)
                                    /\ UpdateTasks(task :> [task EXCEPT !.state = running ])

HandleProcessExit(exit, task) ==
    /\ UpdateTasks(task :> [ task EXCEPT !.state = exited ])
    /\ PublishEvents(<<NewEvent(exit, taskExit)>>)

HandleExit(exit) ==
    /\ UNCHANGED << processes >>
    /\  IF { p \in shimState.running : p = exit } = { } THEN
            /\ UNCHANGED << events, tasks >>
            /\ shimState' = BroadcastExit(shimState, exit)
        ELSE 
            \E p \in { q \in processes : q.pid = exit }:
                    \E tsk \in { q \in tasks: q.id = p.id }:
                        IF tsk.type = exec THEN
                            /\ shimState' = DecRunningExecs(
                                BroadcastExit(RemoveRunning(shimState, exit), exit))
                            /\ HandleProcessExit(exit, tsk)
                        ELSE
                            /\ IF shimState.runningExecs = 0 THEN
                                    /\ shimState' = BroadcastExit(
                                        RemoveRunning(shimState, exit), exit)
                                    /\ HandleProcessExit(exit, tsk)
                                ELSE
                                    /\ UNCHANGED << events, tasks >>
                                    /\ shimState' = BroadcastExit(RemoveRunning(
                                        [ shimState EXCEPT 
                                            !.containerInitExit = shimState.containerInitExit \cup {exit}
                                        ], exit), exit)

\* Processes that have been started, and we can now call `HandleStarted' for:
LOCAL startedProcesses ==
    { p.id : p \in { q \in processes: q.state # starting}} 
        \intersect shimState.startingProcesses

\* Whenever the shim steps, it might do either of these:
ShimService == 
    \/ InitStart
    \/ ExecStart
    \/ \E startingProcess \in startedProcesses:
        /\ HandleStarted(startingProcess)
    \/ NextExit(HandleExit)
    \/ \E initExit \in shimState.containerInitExit:
        /\ UNCHANGED << exits, processes, shimState >>
        /\ shimState.runningExecs = 0
        /\ \E p \in { p \in processes: p.pid = initExit}: 
            /\ \E task \in { t \in tasks: t.id = p.id /\ t.state = running }:
                /\ PublishEvents(<<NewEvent(initExit, taskExit)>>)
                /\ UpdateTasks(task :> [task EXCEPT !.state = exited])


=============================================================================

Shim == INSTANCE RuncShim 

(* A model of the rest of the system (kernel, user, etc.) *)
Environment == INSTANCE Environment

(* We could alias
       Env == Environment!Env /\ UNCHANGED shimState
   but that causes TLC to report all issues with it
   as `Env' issues. This provides better diagnostics: *)
EnvTLC ==
  \/ (Environment!User          /\ UNCHANGED shimState)
  \/ (Environment!Kernel        /\ UNCHANGED shimState)
  \/ (Environment!ProcessDies   /\ UNCHANGED shimState)

-------------------------------------------------------------------------------
\* A complete system

vars == << processes, exits, tasks, events, shimState >>

Init ==
  /\ processes = {}
  /\ exits = <<>>
  /\ tasks = {}
  /\ events = <<>>
  /\ shimState = [ 
        running |-> {},
        startingProcesses |-> {},
        exitSubscribers |-> {},
        runningExecs |-> 0,
        containerInitExit |-> {}
    ]

Next ==
    \/  \/ EnvTLC               \* Either the environment steps, or
        \/ Shim!ShimService     \* the shim steps
    \/  UNCHANGED vars

(* The specification for our shim. This looks like any other property we would want to verify -
    the difference being that contraints defined in the specification define what counts as a 
    valid trace, i.e. how our system is allowed to behave.  On the other hand, invariants/properties
    define constraints that we expect to follow if our system behaves as described in the specification. *)
Spec == 
    /\ Init                 \* Valid initial states
    /\ [][Next]_vars        \* every action should satisfy Next
    (* Some actions are required to happen eventually. For example, a behaviour in
       which the shim stops doing anything forever, even though it could advance 
       some task from the `unstarted' state, isn't a valid behaviour of the system.
       This property is called `weak fairness'. *)
    /\ WF_vars(Shim!ShimService)
    /\ WF_vars(Environment!Kernel /\ UNCHANGED << tasks, events, shimState >>)
    (* We do not require fairness of:
       - `User' (we don't control them),
       - `ProcessDies' (we don't know if/when a process will exit) *)

-------------------------------------------------------------------------------
\* Properties to verify

(* These are properties that should follow automatically if the system behaves as
   described by `Spec' in the previous section. They can be verified by TLC by
   configuring the `INVARIANTS' section of Shim.cfg *)

\* Import this definition from the Enviromment module:
EnvironmentInvariants == Environment!Invariants 

InitExitAlwaysPublished == 
    (* Either `ProcessDies' is eventually-always enabled, i.e. processes are running forever *)
    \/ []<>ENABLED <<Environment!ProcessDies /\ UNCHANGED shimState>>_vars      
    (* Or `User' is eventually-always enabled, i.e. the user won't run all the tasks *)
    \/ []<>ENABLED <<Environment!User /\ UNCHANGED shimState>>_vars     
    (* Or the init exit is eventually published *)
    \/ Environment!InitExitAlwaysPublished       

=============================================================================
