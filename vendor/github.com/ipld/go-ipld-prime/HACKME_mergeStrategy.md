hacking: merge strategies
=========================

This is a short document about how the maintainers of this repo handle branches and merging.
It's useful information for a developer wanting to contribute, but otherwise unimportant.

---

We prefer to:

1. Do development on a branch;
2. Before merge, rebase onto master (if at all possible);
	- if there are individual commit hashes that should be preserved because they've been referenced outside the project, say so; we don't want to have to presume this by default.
	- rebasing your commits (or simply staging them carefully the first time; `git add -p` is your friend) for clarity of later readers is greatly appreciated.
3. Merge, using the "--no-ff" strategy.  The github UI does fine at this.

There are a couple of reasons we prefer this:

- Squashing, if appropriate, can be done by the author.  We don't use github's squash button because it's sometimes quite difficult to make a good combined commit message without effort to do so by the diff's author, so it's best left to that author to do themselves.
- Generating a merge commit gives a good place for github to insert the PR link, if a PR has been used.  This is good info to have if someone is later reading git history and wants to see links to where other discussion may have taken place.
- We *do* like fairly linearly history.  Emphasis on "fairly" -- it doesn't have to be perfectly lock-step ridgidly linear: but when doing `git log --graph`, we also want to not see more than a handful of lines running in parallel at once.  (Too many parallel branches at once is both unpleasant to read and review later, and can indicative of developmental process issues, so it's a good heuristic to minimize for multiple reasons.)  Rebasing before generating a merge commit does this: if consistently done, `git log --graph` will yield two parallel lines at all times.
- Generating a merge commit, when combined with rebasing the commits on the branch right before merge, means `git log --graph` will group up the branch commits in a visually clear way.  Preserving this relation can be useful.  (Neither squashing nor rebase-without-merge approaches preserve this information.)

Mind, all of these rules are heuristics and "rules of thumb".  Some small changes are also perfectly reasonable to land with either a squash or rebase that appends them linearly onto history.

The maintainers may choose strategies as they see fit depending on the size of the content and the level of interest in preserving individual commits and their messages and relational history.


What does this mean for PRs?
----------------------------

- Please keep PRs rebased on top of master as much as possible.

- If you decide you want multiple commits with distinct messages, fine.  If you want to squash, also fine.

- If you are linking to commit hashes and don't want them rebased, please comment about this;
  otherwise, it should not be presumed we'll keep exact commit hashes reachable.
  The maintainers also reserve the right to rebase or squash things at their own option;
  we'll comment explicitly if committing to not do so, and it should not otherwise be presumed.

- If you're not comfortable with rebase: fine.  Just be aware that if a PR branches off master for quite some time,
  and it does become ready for merge later, the maintainers are likely to squash/rebase your work for you before merging.
