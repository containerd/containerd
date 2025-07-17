package utils

// MinCriuVersion for Podman at least CRIU 3.11 is required
const MinCriuVersionPodman = 31100

// PodCriuVersion is the version of CRIU needed for
// checkpointing and restoring containers out of and into Pods.
const PodCriuVersion = 31600
