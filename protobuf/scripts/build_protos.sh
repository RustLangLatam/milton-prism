#!/bin/bash

set -e

# Make sure buf is installed.
#if ! command -v buf &> /dev/null
#then
#    echo "buf could not be found. Please install it with 'brew install buf'."
#    exit
#fi

# Change to the parent directory.
cd "$(dirname "$0")"
cd ..

buf dep update

# Generate code for Rust and Go.
for file in *.gen.yaml
do
    buf generate proto --verbose --template "$file"
done

buf format proto -w

echo "Protos generated successfully."