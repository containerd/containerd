# Add retry and backoff
foreach ( $i in 1..3 ) {
    Write-Output "::group::Attempt $i"
    if ( $i -gt 1 ) {
        # remove any left-over state
        choco uninstall -y --no-progress --force mingw

        Write-Output 'Sleeping for 60 seconds'
        Sleep -Seconds 60
    }

    Write-Output 'manually force remove C:\mingw64'
    Remove-Item -Path "C:\mingw64" -Recurse -Force -ErrorAction Ignore

    choco install -y --no-progress --stop-on-first-failure --force mingw --allow-downgrade --version 12.2.0.3042023
    Write-Output '::endgroup::'
    if ( -not $LASTEXITCODE ) {
        Write-Output "Attempt $i succeeded (exit code: $LASTEXITCODE)"
        break
    }

    Write-Output "::warning title=mingw::Attempt $i failed (exit code: $LASTEXITCODE)"
}

if ( $LASTEXITCODE ) {
    Write-Output "::error::Could not install mingw after $i attempts."
    exit $LASTEXITCODE
}

# Copy to default path
Copy-Item -Path "C:\ProgramData\chocolatey\lib\mingw\tools\install\mingw64" -Destination "C:\mingw64" -Recurse -Force

# Copy as make.exe
$path = "C:\mingw64\bin\mingw32-make.exe" | Get-Item
Copy-Item -Path $path.FullName -Destination (Join-Path $path.Directory.FullName 'make.exe') -Force

# verify mingw32-make was installed
Get-Command -CommandType Application -ErrorAction Stop mingw32-make.exe
