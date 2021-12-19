#!/bin/bash

set -eEuo pipefail

# tested with shellcheck

# extract the gobin for verification
dir=$(go env GOBIN)
dir=${searchdir:-~/go/bin}
if ! [[ -d "${dir}" ]]; then
	echo "GOBIN is incorrectly set, verify it point to a valid directory"
	exit 1
fi

# FORCE variable for auto updating
FORCE=${FORCE:-}

# section to make sure required binaries exist
reuqired_binaries=(
  go
  ugbt
)
missing_binaries=()
## first pass on binaries (so if a bunch of them are missing it'll update once
for required_binary in "${reuqired_binaries[@]}" ; do
  if ! eval "which ${required_binary} 2>&1 >/dev/null"; then
    missing_binaries+=( "$required_binary" ) 
  fi
done
## send bulk message on missing binaires
if [[ "${#missing_binaries[@]}" -ne 0 ]]; then 
	echo "binaries required for operation missing, please install: '${missing_binaries[*]}'"
	exit 1
fi

while IFS= read -r -d '' binary_to_check
do
  echo -n "$(basename "${binary_to_check}"): "
  # get all of the versions
  versions=$(ugbt list "${binary_to_check}" 2>&1 || true )

  # if the command fails to run
  sucess="$?"
  if [[ "${sucess}" -ne 0 ]]; then 
    echo "error getting versions, output '${versions}'"
    continue
  fi

  # if the ugbt notes there is nothing to do
  if [[ "${versions}" == 'no new version' ]]; then
    echo "OK"
    continue
  fi

  # if there are no versions to choose from
  if [[ -z "${versions}" ]]; then 
    echo 'MISSING'
    echo "couldn't get versions"
    continue
  fi

  # if the ugbt fails (this is the error format
  if [[ "${versions}" =~ ugbt:*  ]]; then
    echo "ERROR"
    echo "internal error '${versions}'"
    continue
  fi

  # extract the latest version
  latest_version=$(echo "${versions}" | head -n1 | cut -d' ' -f1)

  echo "UPDATE"
  # if in dry run, print the comnmand that'll upgrade
  if [[ -z "${FORCE}" ]]; then
    echo "ugbt install $binary_to_check $latest_version"
    continue
  fi

  # if in force mode, auto-update
  # wrap with 'true' in case the installation has errors
  ugbt install $binary_to_check $latest_version || true

done < <(find "${dir}" -type f -print0)

echo

# if in dry run, show a way of selectively running the commands
if [[ -z ${FORCE} ]]; then
  echo "to updates (so you can pipe to bash), you can remove the 'FORCE' envvar  or append:" >>/dev/stderr
  echo "grep -A1 UPDATE$ | grep -v -e 'UPDATE$' -e '^--$'" >>/dev/stderr
fi
