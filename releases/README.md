## containerd release process

### containerd CI signal
The [testgrid dashboard](https://testgrid.k8s.io/containerd-periodic) for containerd shows the current state of the
containerd periodic build and test jobs. For cutting a release from branch `release/1.x`, the following should be
verified:
- `containerd-build-1.x` in [containerd-periodics](https://testgrid.k8s.io/containerd-periodic) should be passing.
- [CI](https://github.com/containerd/containerd/actions/workflows/ci.yml) should be green for `release/1.x` branch.

### Steps for making the release

1. Create release pull request with release notes and updated versions.

   1. Review all pull requests merged into the release. For any pull request
      that should be highlighted in the release notes, add the appropriate
      `impact/*` and `area/*` labels. Update the title to match the style used
      in release notes. Normally the titles are short descriptions starting with
      a present tense verb and optionally prepended with square brackets section
      which usually includes release branch.

   2. Compile release notes with summary of changes since the last release and
      add release template file to `releases/` directory. The template is defined
      by containerd's release tool but refer to previous release files for style
      and format help. Name the file using the version, for rc add an `-rc` suffix.
      When moving from rc to final, the rc file may just be renamed and updated.
      See [release-tool](https://github.com/containerd/release-tool)

   3. Update the version file at `https://github.com/containerd/containerd/blob/main/version/version.go`

   4. (major/minor release only) Update RELEASES.md to refer to the new release and dates.

   5. Update the `.mailmap` files for commit authors which have multiple email addresses in the commit log.
      If it is not clear which email or name the contributor might want used in the release notes, reach
      out to the contributor for feedback. NOTE: real names should be used whenever possible. The file is
      maintained by manually adding entries to the file.
      - e.g. `Real Name <preferred@email.com> Other Name <other@email.com>`

   6. Before opening the pull request, run the release tool using the new release notes.
      Ensure the output matches what is expected, including contributors, change log,
      dependencies, and visual elements such as spacing. If a contributor is duplicated,
      use the emails outputted by the release tool to update the mailmap then re-run. The
      goal of the release tool is that is generates release notes that need no
      alterations after it is generated.

2. Create tag

   1. Choose tag for the next release, containerd uses semantic versioning and
      expects tags to be formatted as `vx.y.z[-rc.n]`.

   2. Generate release notes (using a temp file may be helpful).
      - e.g. `release-tool -r -g -d -n -t v1.0.0 ./releases/v1.0.0.toml > /tmp/v1.0.0-notes`
      - if release notes are too long, such as for major/minor releases, add `--skip-commits` option

   3. Create tag using the generated release notes.
      - e.g. `git tag --cleanup=whitespace -s v1.0.0 -F /tmp/v1.0.0-notes`

   4. Verify tag (e.g. `git show v1.0.0`), it may help to compare the new tag against previous.

3. Push tag and Github release

   1. Push the tag to `git@github.com:containerd/containerd.git`.
      NOTE: this will kick off CI to prepare the Github release and binaries.

   2. Verify release job has started in the actions tab and wait for release job to complete
      successfully. If the job fails, attempt to restart it. If the job continuously fails,
      delete the tag from GitHub as soon as possible and fix the issue before restarting the
      release process.

   3. Check the Github release, ensure the content matches what is expected and all the
      release binaries are included.

4. (major/minor release only) Create `release/x.y` branch from tag and push to `git@github.com:containerd/containerd.git`.

5. (patch release only) Update RELEASES.md in main branch to refer to the new patch release.

6. (major/minor release only) Create `cherry-pick/x.y.x` and `cherry-picked/x.y.x` labels

7. Close any milestones associated with the release.

8. Update [`config.toml`](https://github.com/containerd/containerd.io/blob/f827d53826a426cb48f24cc08e43cc8722ad6d01/config.toml#L35) with latest release in the [containerd/containerd.io project](https://github.com/containerd/containerd.io); open PR to
   force website downloads update.

9. Update Kubernetes test infrastructure to test against any new release branches, see example from https://github.com/kubernetes/test-infra/pull/25476

10. Promote on Slack, Twitter, mailing lists, etc
