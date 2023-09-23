# extrace the single specified benchmark results by name
$curr.benchmarkResults[$benchmark] as $c
|
$prev.benchmarkResults[$benchmark] as $p
|
$p
|
[
    # loop through the keys of previous
    # new keys in current are ignored
    keys[]
    |
    if ($c[.]|type)=="number" and ($p[.]|type)=="number" then
        if $p[.]==0 then
            if $c[.]==0 then
                { "\(.)": 0 } # 0 => 0 = 0% change
            else
                { "\(.)": 100 } # 0 => N = 100% change (mathematically should be Inf)
            end
        else
            { "\(.)": ((($c[.]/$p[.]-1)*100)|round) } # % change from previous to current value
        end
    else
        if $c[.]==$p[.] then
            { "\(.)": $p[.] } # output unchanged strings as-is
        else
            { "\(.)": [ $p[.], $c[.] ] } # changed strings become [ previous, current ]
        end
    end
]
|
add