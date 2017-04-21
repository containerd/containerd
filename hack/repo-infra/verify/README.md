# Verification scripts

Collection of scripts that verifies that a project meets requirements set for kubernetes related projects. The scripts are to be invoked depending on the needs via CI tooling, such as Travis CI. See main Readme file on how to integrate the repo-infra in your project.

The scripts are currently being migrated from the main kubernetes repository. If your project requires additional set of verifications, consider creating an issue/PR on repo-infra to avoid code duplication across multiple projects.

If repo-infra is integrated at the root of your project as git submodule at path: `hack/repo-infra`,
then scripts can be invoked as `hack/repo-infra/verify/verify-*.sh`

travis.yaml example:

```
dist: trusty

os:
- linux

language: go

go:
- 1.8

script:
- hack/repo-infra/verify/verify-boilerplate.sh
```

## Verify boilerplate

Verifies that the boilerplate for various formats (go files, Makefile, etc.) is included in each file: `verify-boilerplate.sh`.
