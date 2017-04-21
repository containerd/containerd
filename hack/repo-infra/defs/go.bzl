load("@io_bazel_rules_go//go:def.bzl", "go_env_attrs")

go_filetype = ["*.go"]

def _compute_genrule_variables(resolved_srcs, resolved_outs):
  variables = {"SRCS": cmd_helper.join_paths(" ", resolved_srcs),
               "OUTS": cmd_helper.join_paths(" ", resolved_outs)}
  if len(resolved_srcs) == 1:
    variables["<"] = list(resolved_srcs)[0].path
  if len(resolved_outs) == 1:
    variables["@"] = list(resolved_outs)[0].path
  return variables

def _go_sources_aspect_impl(target, ctx):
  transitive_sources = set(target.go_sources)
  for dep in ctx.rule.attr.deps:
    transitive_sources = transitive_sources | dep.transitive_sources
  return struct(transitive_sources = transitive_sources)

go_sources_aspect = aspect(
    attr_aspects = ["deps"],
    implementation = _go_sources_aspect_impl,
)

def _compute_genrule_command(ctx):
  cmd = [
      'set -e',
      # setup GOROOT
      'export GOROOT=$$(pwd)/' + ctx.file.go_tool.dirname + '/..',
      # setup main GOPATH
      'export GOPATH=/tmp/gopath',
      'export GO_WORKSPACE=$${GOPATH}/src/' + ctx.attr.go_prefix.go_prefix,
      'mkdir -p $${GO_WORKSPACE%/*}',
      'ln -s $$(pwd) $${GO_WORKSPACE}',
      # setup genfile GOPATH
      'export GENGOPATH=/tmp/gengopath',
      'export GENGO_WORKSPACE=$${GENGOPATH}/src/' + ctx.attr.go_prefix.go_prefix,
      'mkdir -p $${GENGO_WORKSPACE%/*}',
      'ln -s $$(pwd)/$(GENDIR) $${GENGO_WORKSPACE}',
      # drop into WORKSPACE
      'export GOPATH=$${GOPATH}:$${GENGOPATH}',
      'cd $${GO_WORKSPACE}',
      # execute user command
      ctx.attr.cmd.strip(' \t\n\r'),
  ]
  return '\n'.join(cmd)

def _go_genrule_impl(ctx):
  all_srcs = set(ctx.files.go_src)
  label_dict = {}

  for dep in ctx.attr.go_deps:
    all_srcs = all_srcs | dep.transitive_sources

  for dep in ctx.attr.srcs:
    all_srcs = all_srcs | dep.files
    label_dict[dep.label] = dep.files

  cmd = _compute_genrule_command(ctx)

  resolved_inputs, argv, runfiles_manifests = ctx.resolve_command(
      command=cmd,
      attribute="cmd",
      expand_locations=True,
      make_variables=_compute_genrule_variables(all_srcs, set(ctx.outputs.outs)),
      tools=ctx.attr.tools,
      label_dict=label_dict
  )

  ctx.action(
      inputs = list(all_srcs) + resolved_inputs,
      outputs = ctx.outputs.outs,
      env = ctx.configuration.default_shell_env,
      command = argv,
      progress_message = "%s %s" % (ctx.attr.message, ctx),
      mnemonic = "GoGenrule",
  )

# We have codegen procedures that depend on the "go/*" stdlib packages
# and thus depend on executing with a valid GOROOT and GOPATH containing
# some amount transitive go src of dependencies. This go_genrule enables
# the creation of these sandboxes.
go_genrule = rule(
    attrs = go_env_attrs + {
        "srcs": attr.label_list(allow_files = True),
        "tools": attr.label_list(
            cfg = "host",
            allow_files = True,
        ),
        "outs": attr.output_list(mandatory = True),
        "cmd": attr.string(mandatory = True),
        "go_deps": attr.label_list(
            aspects = [go_sources_aspect],
        ),
        "message": attr.string(),
        "executable": attr.bool(default = False),
    },
    output_to_genfiles = True,
    implementation = _go_genrule_impl,
)
