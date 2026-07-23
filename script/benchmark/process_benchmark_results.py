#!/usr/bin/env python3

#   Copyright The containerd Authors.
#
#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at
#
#       http://www.apache.org/licenses/LICENSE-2.0
#
#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.
#

"""Processes containerd benchmark results from the periodic benchmarking workflow.
"""

import argparse
import copy
import collections
import enum
import functools
import itertools
import math
import os
import re

from matplotlib import pyplot
from matplotlib import colors as mcolors
from matplotlib import patheffects
import numpy
import yaml


DEFAULT_BENCHMARKS_PARAMS_FILENAME = "benchmark-run-params.yaml"
DEFAULT_BENCHMARKS_METRICS_DIRNAME = "benchmarks"

MARKDOWN_TABLE_HEADER_ROW = (
    "| Metric | Fastest | Average | Slowest | 95th% | 50th% |")
MARKDOWN_TABLE_ROW_OUTPUT_FORMAT = (
    "| %(metric_name)s | %(fastest).6fs | %(mean).6fs "
    "| %(slowest).6fs | %(95th).6fs | %(50th).6fs |")

BENCHMARK_LABEL_NAME_FORMAT = (
    "%(metric_name)s: %(os_distro)s %(os_release)s; %(azure_vm_size)s\n"
    "ContainerD %(containerd_tag)s; %(runtime_tag)s; (RunID %(run_id)s)"
)


MARKER_COLORS = list(mcolors.BASE_COLORS)
# NOTE: selected the most discenarble symbols:
MARKER_POINTS = [".", "+", "x", "s", "p", "P", "D", "*", "v", "^"]
MARKER_COLOR_COMBOS = list(itertools.product(MARKER_POINTS, MARKER_COLORS))


class GraphYAxisResolution(enum.Enum):
    """ Labels various Y axis sizes (in seconds). """
    AUTO = 0
    XXS = 0.1
    XS = 0.25
    S = 0.5
    M = 1
    L = 5
    XL = 10
    XXL = 30
    XXXL = 60


ALL_Y_LIMITS = (
    GraphYAxisResolution.XXS, GraphYAxisResolution.XS, GraphYAxisResolution.S,
    GraphYAxisResolution.M, GraphYAxisResolution.L, GraphYAxisResolution.XL,
    GraphYAxisResolution.XXL, GraphYAxisResolution.XXXL,
    GraphYAxisResolution.AUTO)


def _get_y_ticks_for_limit(limit, ticks_per_second=10, lower_limit=0):
    """ Returns a list of (tick_value_ns, tick_lavel) to be used for setting yticks.
    """
    if isinstance(limit, GraphYAxisResolution):
        limit = limit.value

    # NOTE(aznashwan): find better way to avoid spamming ticks for high ranges:
    if limit >= 5:
        ticks_per_second = 1

    ytick_step_size = math.floor((10 ** 9) / ticks_per_second)
    # NOTE(aznashwan): find better way to ensure decent ticks for low limits:
    if ytick_step_size <= limit:
        ytick_step_size = math.floor(limit * (10 ** 9) / 2)

    ytick_values = list(range(lower_limit, math.ceil(limit * (10 ** 9)), ytick_step_size))
    ytick_labels = ["%.2f" % (val/(10 ** 9)) for val in ytick_values]

    return ytick_values, ytick_labels

    # ticks = [x * (10 ** 9) for x in range(0, math.ceil(y_axis_max.value))]
    # last_tick = y_axis_max.value
    # if ticks[-1] != last_tick:
    #     ticks.append(last_tick)
    # axes.set_yticks(ticks)


def _add_legend_table_for_datasets(
        axes, marker_dataset_map,
        preferred_column_title_param_name="osRelease"):
    """ Given a mapping between the marker and the datasets for it,
    adds a table to the provided axes with the benchmark run specs
    to the given axes.
    """
    # Use an OrderedDict to preserve iteration order:
    marker_dataset_map = collections.OrderedDict(marker_dataset_map)

    # Ensure all keys present for all run parameter sets:
    run_params_keys = []
    for metric_values in marker_dataset_map.values():
        run_params = metric_values["benchmark_run_params"]
        if not run_params_keys:
            run_params_keys = list(run_params.keys())

        if not all(k in run_params for k in run_params_keys):
            raise ValueError(
                f"One or more required run param keys ({run_params_keys}) "
                "are missing from a dataset: {metric_values}")

    # The title column will simply be the run parameters:
    title_column = sorted(run_params_keys)

    # The title row will be 'preferred_column_title_param_name' of each dataset:
    title_row = [
        mds["benchmark_run_params"][preferred_column_title_param_name]
        for mds in marker_dataset_map.values()]
    title_row_colors = [mc for (_, mc) in marker_dataset_map]

    # Table data is a row with the values for a given param name taken from each dataset:
    table_data = [
        [str(mds["benchmark_run_params"][param_name])[:20] for mds in marker_dataset_map.values()]
        for param_name in title_column]

    # Define each row:
    table = axes.table(
        cellText=table_data, rowLabels=title_column, colLabels=title_row,
        colColours=title_row_colors, bbox=[0, -0.35, 1, 0.275])

    return table


def _get_outlined_line_style_kwargs(
        color, outline_color='k', base_line_width=1, outline_width=3):
    """ Returns a dict with all the kwargs required to be passed to `matplotlib.plot()`
    for defining a dichromatic line with the given properties.
    """
    if outline_width <= base_line_width:
        # NOTE: the width of the "underline" which will provide the outline should
        # always greater than the width of the line itself:
        outline_width = math.floor(base_line_width) + 1

    return {
        "color": color,
        "linewidth": base_line_width,
        "path_effects": [patheffects.Stroke(
            linewidth=outline_width, foreground=outline_color), patheffects.Normal()]}


def _add_args(parser):
    def _check_dir_arg(argname, value):
        if not os.path.isdir(value):
            raise argparse.ArgumentTypeError(
                f"provided '{argname}' is not a directory: '{value}'")
        return value
    parser.add_argument(
        "benchmarks_directories", metavar="/path/to/benchmarks/dir/1 ...",
        nargs='+', action='extend',
        type=lambda p: _check_dir_arg("benchmarks_root_dir", p),
        help="String path to the directory containing the output(s) of "
             "the benchmarking workflow(s).")
    parser.add_argument(
        "--output-dir", metavar="/path/to/output_dir", default=".",
        type=lambda p: _check_dir_arg("--output-dir", p),
        help="String path to the directory to output benchmark plots to. "
             "Defaults to current working dir.")
    return parser


def _get_bechmark_graph_label(metric_name, benchmark_run_params):
    return BENCHMARK_LABEL_NAME_FORMAT % {
        "metric_name": metric_name,
        "os_distro": benchmark_run_params.get("osDistro", "unknown OS").capitalize(),
        "os_release": benchmark_run_params.get("osRelease", "unknown Release"),
        "containerd_tag": benchmark_run_params.get("containerdCommit", "unknown")[:9],
        "runtime_tag": benchmark_run_params.get("runtimeTag", "unknown Runtime"),
        "azure_vm_size": benchmark_run_params.get("azureVmSize", "unknown"),
        "run_id": str(benchmark_run_params.get("workflowRunId", "unknown"))}


def _load_yaml_file(filepath):
    with open(filepath, 'r') as fin:
        return yaml.safe_load(fin)


def _convert_to_ms(string_val):
    """ Converts the provided string benchmark metric to float in milliseconds. """
    if string_val.endswith("ms"):
        # do nothing, is ms
        return float(string_val.strip("ms"))
    if string_val.endswith("µs"):
        return float(string_val.strip("µs")) / 1000
    if string_val.endswith("s"):
        return float(string_val.strip("s")) * 1000
    return float(string_val)


def _get_stats_for_metric_values(values):
    return {
        "mean": numpy.mean(values),
        "slowest": numpy.max(values),
        "fastest": numpy.min(values),
        "95th": numpy.percentile(values, 95),
        "50th": numpy.percentile(values, 50)}


def _generate_markdown_table(metrics_stats, outpath):
    """ Outputs a markdown with the given stats to the given filepath. """
    table = MARKDOWN_TABLE_HEADER_ROW
    for metric_name, stats in metrics_stats.items():
        stats.update({"metric_name": metric_name})
        table = "\n".join([table, MARKDOWN_TABLE_ROW_OUTPUT_FORMAT % stats])

    with open(f"{outpath}.md", "w") as fout:
        fout.write(table)


def _new_figure_with_axes(
        axes_title, xlabel, ylabel, dpi=120, figsize=(10, 10), pad_inches=1, clear=True):
    figure, axes = pyplot.subplots(figsize=figsize, dpi=dpi, clear=clear)
    axes.set_title(axes_title)
    axes.set_xlabel(xlabel)
    axes.set_ylabel(ylabel)
    return figure, axes


def _normalize_filename(filename):
    return re.sub("_+", "_", re.sub("[.:; \n]{1}", "_", filename))


def _save_figure(figure, outdir, title, plot_format="png"):
    # Normalize the title name:
    outfile = os.path.join(outdir, f"{_normalize_filename(title)}.png")
    figure.tight_layout()
    figure.savefig(outfile, format=plot_format)
    print(f"Generated: {outfile}")


def _add_value_sets_to_axes(
        axes, metric_name, metric_datasets, x_axis_max=None, y_axis_max=None,
        marker_color_combos=MARKER_COLOR_COMBOS):
    """ Adds all of the given value sets to the provided Axes.

    :param dict value_sets_map: mapping between metric legend entries and value sets.
    """
    # Determine maximum X axis input:
    normalized_value_sets_map = {
        _get_bechmark_graph_label(
            f"{metric_name} {y_axis_max.name}",
            metric_dataset['benchmark_run_params']): metric_dataset
        for metric_dataset in metric_datasets}

    if not x_axis_max:
        x_axis_max = max(
            map(lambda mds: len(mds['datapoints']), normalized_value_sets_map.values()))

    # Normalize all values greater than y_axis_max, if provided:
    if y_axis_max.value:
        ymax = y_axis_max.value * (10 ** 9)
        # Set Y axis limit:
        axes.set_ylim(bottom=0, top=ymax)

        def _normalize_value_set(valset):
            valset = copy.deepcopy(valset)
            valset['original_datapoints'] = valset['datapoints']
            valset['datapoints'] = list(
                map(functools.partial(min, ymax), valset['datapoints']))
            return valset
        normalized_value_sets_map = {
            label: _normalize_value_set(valset)
            for label, valset in normalized_value_sets_map.items()}

        # Update Y axis ticks:
        ytick_values, ytick_labels = _get_y_ticks_for_limit(
            y_axis_max, ticks_per_second=5, lower_limit=0)
        axes.set_yticks(ytick_values)
        axes.set_yticklabels(ytick_labels)

    marker_index = 0
    legend_marker_dataset_map = {}
    for legend_entry, metric_values in normalized_value_sets_map.items():
        marker_style, marker_color = marker_color_combos[
            marker_index % len(marker_color_combos)]
        legend_marker_dataset_map[(marker_style, marker_color)] = metric_values

        precomputed_stats = None
        if metric_values.get('original_datapoints'):
            precomputed_stats = _get_stats_for_metric_values(
                metric_values.get('original_datapoints'))
        axes = _add_value_set_to_axes(
            axes, metric_name, metric_values['datapoints'], marker_style=marker_style,
            marker_color=marker_color, precomputed_stats=precomputed_stats)

        marker_index = marker_index + 1
        print(f"Added value set '{legend_entry}' to axes {axes}")

    # Add legend to table:
    _ = _add_legend_table_for_datasets(axes, legend_marker_dataset_map)

    return axes


def _add_value_set_to_axes(
        axes, metric_name, metric_values, marker_style=".", marker_color="blue",
        color50th="green", color95th="red", precomputed_stats=None):
    # X axis is simply the index (aka sequential number) of each datapoint:
    xaxis = range(1, len(metric_values)+1)
    axes.scatter(xaxis, metric_values, marker=marker_style, c=marker_color)

    # Plot percentile lines:
    stats = precomputed_stats
    if not precomputed_stats:
        stats = _get_stats_for_metric_values(metric_values)
    axes.axhline(
        stats['50th'], label=f"50th%%: {stats['50th']}",
        **_get_outlined_line_style_kwargs(marker_color, outline_color=color50th))
    axes.axhline(
        stats['95th'], label=f"95th%%: {stats['95th']}",
        **_get_outlined_line_style_kwargs(marker_color, outline_color=color95th))

    return axes


def _normalize_results_data(results_data):
    """Normalizes the provided results data to a simple mapping between the
    names of operations and its respective datapoints in an ordered list.
    """
    datapoints = results_data.get("datapoints")
    if not datapoints:
        # using v1 version of the output which is already properly-formatted:
        return results_data

    operation_names = results_data.get("operationsNames")
    if not operation_names:
        raise ValueError(
            f"Missing 'operationsNames' from results set: {results_data}", results_data)

    result = {}
    for i, operation_name in enumerate(operation_names):
        result[operation_name] = [
            math.fabs(point.get("operationsDurationsNs")[i]) for point in datapoints]

    return result


def _load_benchmarks_directory(input_dir):
    """ Loads benchmark parameters and metrics data from given directory.
    Returns a dict of the form: {
        "input_dir": "{input_dir}",
        "benchmark_run_params": {"benchmark": "run_params"...},
        "benchmark_files": {
            "benchmark_filename_1.json": {"benchmark": "data"...}
        }
    }
    """
    res = {}
    benchmark_run_params_file = os.path.join(input_dir, DEFAULT_BENCHMARKS_PARAMS_FILENAME)
    if not os.path.isfile(benchmark_run_params_file):
        raise Exception(
            f"Could not find benchmark params file {benchmark_run_params_file}"
            "in the provided benchmark directory: {input_dir}")
    res['input_dir'] = input_dir
    res['benchmark_run_params'] = _load_yaml_file(benchmark_run_params_file)

    # Check directory for actual benchmarks.
    benchmarks_dir = os.path.join(input_dir, DEFAULT_BENCHMARKS_METRICS_DIRNAME)
    if not os.path.isdir(benchmarks_dir):
        # NOTE: some Windows benchmarks used `Benchmarks` (capitalized):
        benchmarks_dir = os.path.join(input_dir, DEFAULT_BENCHMARKS_METRICS_DIRNAME.capitalize())

    if not os.path.isdir(benchmarks_dir):
        raise Exception(
            f"Could not find benchmarks dir '{benchmarks_dir}' (capitalized or otherwise) "
            "within the provided input dir '{input_dir}'.")

    # Load all metrics files:
    res['benchmark_files'] = {}
    for metrics_file_name in os.listdir(benchmarks_dir):
        mfilepath = os.path.join(benchmarks_dir, metrics_file_name)
        if not os.path.isfile(mfilepath):
            print(f"Skipping following non-file: {mfilepath}")
            continue

        print(f"Loading metrics file '{mfilepath}'")
        res['benchmark_files'][metrics_file_name] = _load_yaml_file(mfilepath)

    return res


def _itemize_results_by_operation(benchmark_directory_data):
    """ Given the benchmark data loaded from a directory by
    _load_benchmarks_directory, returns a dict whose keys are
    the operations being benchmarked: {
        "operation_name_1": [{
            "input_dir": "{input_dir}",
            "datapoints": [datapoint1, datapoint2, ...],
            "benchmark_run_params": "{benchmark_run_params}",
            "benchmark_file": "benchmark_filename.json"
        }]
    }
    """
    # Check all keys present:
    input_dir = benchmark_directory_data.get('input_dir')
    benchmark_run_params = benchmark_directory_data.get('benchmark_run_params')
    benchmark_files = benchmark_directory_data.get('benchmark_files')
    if not all(
            param for param in [input_dir, benchmark_run_params, benchmark_files]):
        raise ValueError(f"One or more missing required keys: {benchmark_directory_data}")

    # Iterate through all files, normalize data, then add entry for each operation:
    res = {}
    for filename, filedata in benchmark_files.items():
        normalized_metric_values = _normalize_results_data(filedata)
        for operation_name, datapoints in normalized_metric_values.items():
            if not res.get(operation_name):
                res[operation_name] = []

            res[operation_name].append({
                "input_dir": input_dir,
                "datapoints": datapoints,
                "benchmark_run_params": benchmark_run_params,
                "benchmark_file": filename})

    return res


def _process_data_for_metric(
        metric_name, metric_datasets, outdir, generate_markdown=True, dpi=120,
        pad_inches=1, figsize=(10, 10), ylimits=ALL_Y_LIMITS):
    """ Processes the data sets for the given metric, outputting all relevant graph
    and markdown output files in the provided `outdir`.
    """
    title = metric_name
    if len(metric_name) > 1:
        title = f"{metric_name} Comparison"

    # Iterate through each requested graph size and metric set:
    for ylimit in ylimits:
        figure, axes = _new_figure_with_axes(
            title, xlabel="iteration #", ylabel="time: s",
            dpi=dpi, figsize=figsize, pad_inches=pad_inches, clear=True)

        axes = _add_value_sets_to_axes(
            axes, metric_name, metric_datasets, y_axis_max=ylimit)

        figure.subplots_adjust(left=0.2, bottom=0.2)

        filename = _get_bechmark_graph_label(
            f"{metric_name} {ylimit.name}",
            # TODO(aznashwan): better general title:
            metric_datasets[0]['benchmark_run_params'])
        _save_figure(figure, outdir, filename)

    # Optionally generate a markdown file for each stat:
    if generate_markdown:
        stats_to_add = {}
        mdfile = os.path.join(outdir, _normalize_filename(f"stats-{metric_name}"))

        for metric_dataset in metric_datasets:
            label = _get_bechmark_graph_label(
                metric_name, metric_dataset['benchmark_run_params'])
            stats_to_add[label] = _get_stats_for_metric_values(
                metric_dataset['datapoints'])

        _generate_markdown_table(stats_to_add, mdfile)


def _process_benchmark_directories(benchmark_directories, outdir):
    # Aggregate results from all benchmark directories:
    operations_benchmark_data = {}
    for bdir in benchmark_directories:
        benchmark_directory_data = _load_benchmarks_directory(bdir)
        operations_data = _itemize_results_by_operation(benchmark_directory_data)

        for operation_name, operation_results in operations_data.items():
            if operation_name not in operations_benchmark_data:
                operations_benchmark_data[operation_name] = operation_results
            else:
                operations_benchmark_data[operation_name].extend(operation_results)

    for metric_name, metric_datasets in operations_benchmark_data.items():
        _process_data_for_metric(metric_name, metric_datasets, outdir)


def main():
    parser = argparse.ArgumentParser(
        description="Process ContainerD benchmarking results.")
    _add_args(parser)
    args = parser.parse_args()

    _process_benchmark_directories(args.benchmarks_directories, args.output_dir)


if __name__ == "__main__":
    main()
