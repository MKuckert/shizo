#!/usr/bin/env bash

if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <feature-name>"
    exit 1
fi

FEATURE=$1
PLAN="plans/drafts/PLAN-${FEATURE}.md"
BRANCH="feature/${FEATURE}"
WORKDIR="../feature-${FEATURE}"

if [ ! -f "${PLAN}" ]; then
    echo "Plan file ${PLAN} does not exist."
    exit 2
fi

git branch "${BRANCH}"
git worktree add "${WORKDIR}" "${BRANCH}"
cd "${WORKDIR}"
~/.shizo/engine/shizo attest
./run_harness.sh "Use ponytail skill. \
\
Implement ${FEATURE} as described in ${PLAN}.\
Follow the plan step by step. If you encounter any issues, refer to the plan for guidance or ask the user."
