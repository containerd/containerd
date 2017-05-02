#define _GNU_SOURCE
#include <signal.h>
#include <sys/signalfd.h>
#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/types.h>
#include <sys/resource.h>
#include <sys/wait.h>

// that allows it to be repartented to init and handle reattach from the caller.
//
// the cwd of the shim should be the bundle for the container.  Arg1 should be the path
// to the state directory where the shim can locate fifos and other information.
void shim_handle_error(int err)
{
if (err!=0) 
	{
	fprintf(stderr, "shim error %d \n", strerror(errno));
	exit(-1);
	}
}

int shim_get_signal()
{
int shim_signal;
int err;
sigset_t sigset;

//set up signal handler
err=sigemptyset(&sigset);
shim_handle_error(err);

// get signal child
err=sigaddset(&sigset, SIGCHLD);
shim_handle_error(err);

// no clue but has to be done
err=sigprocmask(SIG_BLOCK, &sigset, NULL); 
shim_handle_error(err);

// assign signalfd
shim_signal=signalfd(-1, &sigset,SFD_CLOEXEC);

return shim_signal;
}

void shim_signal_reap(pid_t shim_pid_child)
{
int status;
pid_t shim_pid;

struct rusage process_info;

for(;;)
	{
	shim_pid=wait4(-1, &status, WNOHANG, &process_info);
	if ((shim_pid>0) && (shim_pid==shim_pid_child)) exit(0);
	}
}


//launch child process and reap all its children 
int main(int argv,char **argc) 
{
int err;
int shim_signal;
pid_t shim_pid;
char** temp_argc=&argc[1];

//create fd to listen to all signals
shim_signal=shim_get_signal();
	
//fork off child process to run the user provided command
shim_pid=fork();

//check if in the child process
// if in child execute the user provided command
if(shim_pid == 0) {
     	err = execvp(temp_argc[0],temp_argc);
	shim_handle_error(err);
	exit(0);
}

// handle signals in the container for the child process
for(;;)
	{
	struct signalfd_siginfo shim_signalfd_info;
	read(shim_signal, &shim_signalfd_info, sizeof(shim_signalfd_info));
	shim_signal_reap(shim_pid);
	}

}


