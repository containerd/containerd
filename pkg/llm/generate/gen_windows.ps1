#!powershell

$ErrorActionPreference = "Stop"

function init_vars {
    $script:llamacppDir = "../llama.cpp"
    $script:cmakeDefs = @("-DBUILD_SHARED_LIBS=on", "-DLLAMA_NATIVE=off",  "-A","x64")
    $script:cmakeTargets = @("ext_server")
    $script:ARCH = "amd64" # arm not yet supported.
    if ($env:CGO_CFLAGS -contains "-g") {
        $script:cmakeDefs += @("-DCMAKE_VERBOSE_MAKEFILE=on", "-DLLAMA_SERVER_VERBOSE=on")
        $script:config = "RelWithDebInfo"
    } else {
        $script:cmakeDefs += @("-DLLAMA_SERVER_VERBOSE=off")
        $script:config = "Release"
    }
    # Try to find the CUDA dir
    if ($env:CUDA_LIB_DIR -eq $null) {
        $d=(get-command -ea 'silentlycontinue' nvcc).path
        if ($d -ne $null) {
            $script:CUDA_LIB_DIR=($d| split-path -parent)
        }
    } else {
        $script:CUDA_LIB_DIR=$env:CUDA_LIB_DIR
    }
    $script:GZIP=(get-command -ea 'silentlycontinue' gzip).path
    $script:DUMPBIN=(get-command -ea 'silentlycontinue' dumpbin).path
    if ($null -eq $env:CMAKE_CUDA_ARCHITECTURES) {
        $script:CMAKE_CUDA_ARCHITECTURES="50;52;61;70;75;80"
    } else {
        $script:CMAKE_CUDA_ARCHITECTURES=$env:CMAKE_CUDA_ARCHITECTURES
    }
}

function git_module_setup {
    # TODO add flags to skip the init/patch logic to make it easier to mod llama.cpp code in-repo
    & git submodule init
    if ($LASTEXITCODE -ne 0) { exit($LASTEXITCODE)}
    & git submodule update --force "${script:llamacppDir}"
    if ($LASTEXITCODE -ne 0) { exit($LASTEXITCODE)}
}

function apply_patches {
    # Wire up our CMakefile
    if (!(Select-String -Path "${script:llamacppDir}/examples/server/CMakeLists.txt" -Pattern 'ollama')) {
        Add-Content -Path "${script:llamacppDir}/examples/server/CMakeLists.txt" -Value 'include (../../../ext_server/CMakeLists.txt) # ollama'
    }

    # Apply temporary patches until fix is upstream
    $patches = Get-ChildItem "../patches/*.diff"
    foreach ($patch in $patches) {
        # Extract file paths from the patch file
        $filePaths = Get-Content $patch.FullName | Where-Object { $_ -match '^\+\+\+ ' } | ForEach-Object {
            $parts = $_ -split ' '
            ($parts[1] -split '/', 2)[1]
        }

        # Checkout each file
        foreach ($file in $filePaths) {
            Set-Location -Path ${script:llamacppDir}
            git checkout $file
        }
    }

    # Apply each patch
    foreach ($patch in $patches) {
        Set-Location -Path ${script:llamacppDir}
        git apply $patch.FullName
    }

    # Avoid duplicate main symbols when we link into the cgo binary
    $content = Get-Content -Path "${script:llamacppDir}/examples/server/server.cpp"
    $content = $content -replace 'int main\(', 'int __main('
    Set-Content -Path "${script:llamacppDir}/examples/server/server.cpp" -Value $content
}

function build {
    write-host "generating config with: cmake -S ${script:llamacppDir} -B $script:buildDir $script:cmakeDefs"
    & cmake --version
    & cmake -S "${script:llamacppDir}" -B $script:buildDir $script:cmakeDefs
    if ($LASTEXITCODE -ne 0) { exit($LASTEXITCODE)}
    write-host "building with: cmake --build $script:buildDir --config $script:config ($script:cmakeTargets | ForEach-Object { "--target", $_ })"
    & cmake --build $script:buildDir --config $script:config ($script:cmakeTargets | ForEach-Object { "--target", $_ })
    if ($LASTEXITCODE -ne 0) { exit($LASTEXITCODE)}
}

function install {
    rm -ea 0 -recurse -force -path "${script:buildDir}/lib"
    md "${script:buildDir}/lib" -ea 0 > $null
    cp "${script:buildDir}/bin/${script:config}/ext_server.dll" "${script:buildDir}/lib"
    cp "${script:buildDir}/bin/${script:config}/llama.dll" "${script:buildDir}/lib"

    # Display the dll dependencies in the build log
    if ($script:DUMPBIN -ne $null) {
        & "$script:DUMPBIN" /dependents "${script:buildDir}/bin/${script:config}/ext_server.dll" | select-string ".dll"
    }
}

function compress_libs {
    if ($script:GZIP -eq $null) {
        write-host "gzip not installed, not compressing files"
        return
    }
    write-host "Compressing dlls..."
    $libs = dir "${script:buildDir}/lib/*.dll"
    foreach ($file in $libs) {
        & "$script:GZIP" --best -f $file
    }
}

function cleanup {
    Set-Location "${script:llamacppDir}/examples/server"
    git checkout CMakeLists.txt server.cpp
}

init_vars
git_module_setup
apply_patches

# -DLLAMA_AVX -- 2011 Intel Sandy Bridge & AMD Bulldozer
# -DLLAMA_F16C -- 2012 Intel Ivy Bridge & AMD 2011 Bulldozer (No significant improvement over just AVX)
# -DLLAMA_AVX2 -- 2013 Intel Haswell & 2015 AMD Excavator / 2017 AMD Zen
# -DLLAMA_FMA (FMA3) -- 2013 Intel Haswell & 2012 AMD Piledriver

$script:commonCpuDefs = @("-DCMAKE_POSITION_INDEPENDENT_CODE=on", "-DLLAMA_NATIVE=off")

$script:cmakeDefs = $script:commonCpuDefs + @("-DLLAMA_AVX=off", "-DLLAMA_AVX2=off", "-DLLAMA_AVX512=off", "-DLLAMA_FMA=off", "-DLLAMA_F16C=off") + $script:cmakeDefs
$script:buildDir="${script:llamacppDir}/build/windows/${script:ARCH}/cpu"
write-host "Building LCD CPU"
build
install
compress_libs

$script:cmakeDefs = $script:commonCpuDefs + @("-DLLAMA_AVX=on", "-DLLAMA_AVX2=off", "-DLLAMA_AVX512=off", "-DLLAMA_FMA=off", "-DLLAMA_F16C=off") + $script:cmakeDefs
$script:buildDir="${script:llamacppDir}/build/windows/${script:ARCH}/cpu_avx"
write-host "Building AVX CPU"
build
install
compress_libs

$script:cmakeDefs = $script:commonCpuDefs + @("-DLLAMA_AVX=on", "-DLLAMA_AVX2=on", "-DLLAMA_AVX512=off", "-DLLAMA_FMA=on", "-DLLAMA_F16C=on") + $script:cmakeDefs
$script:buildDir="${script:llamacppDir}/build/windows/${script:ARCH}/cpu_avx2"
write-host "Building AVX2 CPU"
build
install
compress_libs

if ($null -ne $script:CUDA_LIB_DIR) {
    # Then build cuda as a dynamically loaded library
    $nvcc = (get-command -ea 'silentlycontinue' nvcc)
    if ($null -ne $nvcc) {
        $script:CUDA_VERSION=(get-item ($nvcc | split-path | split-path)).Basename
    }
    if ($null -ne $script:CUDA_VERSION) {
        $script:CUDA_VARIANT="_"+$script:CUDA_VERSION
    }
    init_vars
    $script:buildDir="${script:llamacppDir}/build/windows/${script:ARCH}/cuda$script:CUDA_VARIANT"
    $script:cmakeDefs += @("-DLLAMA_CUBLAS=ON", "-DLLAMA_AVX=on", "-DCMAKE_CUDA_ARCHITECTURES=${script:CMAKE_CUDA_ARCHITECTURES}")
    build
    install
    cp "${script:CUDA_LIB_DIR}/cudart64_*.dll" "${script:buildDir}/lib"
    cp "${script:CUDA_LIB_DIR}/cublas64_*.dll" "${script:buildDir}/lib"
    cp "${script:CUDA_LIB_DIR}/cublasLt64_*.dll" "${script:buildDir}/lib"
    compress_libs
}
# TODO - actually implement ROCm support on windows
$script:buildDir="${script:llamacppDir}/build/windows/${script:ARCH}/rocm"

rm -ea 0 -recurse -force -path "${script:buildDir}/lib"
md "${script:buildDir}/lib" -ea 0 > $null
echo $null >> "${script:buildDir}/lib/.generated"

cleanup
write-host "`ngo generate completed"