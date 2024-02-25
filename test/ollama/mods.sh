#!/bin/bash

for i in {1..100}; do
    /opt/homebrew/bin/mods -f "Write me a story about love." &
done

# Wait for all background jobs to finish
wait
