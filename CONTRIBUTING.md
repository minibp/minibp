## Before contributing

This guideline explains how to contribute to minibp. You **MUST** read this before writing or refactoring any code.

When contributing to this project (via `git push` , via Pull Request and cerry-pick patch), these guidelines apply, and you are held responsible for the code you commit.

Last updated date: Please refer to the commit history. 

Please note that **we do not track changes to CONTRIBUTING.md, so kindly keep this in mind**.

## 1. About the AI code

See [ai part of tree-sitter contributing guide](https://github.com/tree-sitter/tree-sitter/blob/475c48d1e3140540296cce87eb5ab1e8160d5d17/docs/src/6-contributing.md#ai-policy)

Extra: For a large number (>=5) of malicious AI-generated PRs, we will close them directly because we feel that you have no sincerity at all and just hope to gain community honor and praise without putting in any effort (which doesn't seem quite right, at least the people who initiated these have spent money, whether implicitly or explicitly).

## 2. About the commit

Commit messages should be concise and clear, with the first letter capitalized, and the title should not exceed 50 characters.

If a body is needed, be sure to explain the "what," "why," and "how." The body should not exceed 200 characters.

Please note, do not use [Conventional Commits](https://www.conventionalcommits.org/) as the standard for commit messages.We believe this will affect the efficiency of reading changes. In general, this kind of submission method is more suitable for newcomers who don’t know any rules.

If a change has a corresponding original author, under any circumstances you **MUST** use git am and git cherry-pick to **fully and completely preserve all the patch information unchanged**. If a patch cannot be applied due to merge conflicts, please merge manually.

If you intentionally submit code with backdoors, vulnerabilities, or viruses, we will report and block your GitHub account on the grounds of spam or abuse.


## 3. About the issues, pull requests and contributors

Issues and features should be documented and archived in the form of an issue.

Before initiating a major change, please open an issue first, and then link to that issue in the pull request.

Merge requests can only use rebase merges to preserve contributor information. If a merge request has multiple authors, please consider either crediting these authors in a single commit or splitting them into multiple commits with different authors.

The submission of any meaningless or extraneous information within issues and pull requests is absolutely forbidden. Offending comments, issues, or pull requests may be closed and locked, deleted, or reported as appropriate.

Regarding updates to CONTRIBUTING.md and SECURITY.md, you not only need to explain the reasons for the updates, but also ensure that the updates do not disrupt the existing order or have minimal impact. Generally, as long as the changes are reasonable, they will be approved. If contributors have objections to certain changes, you are obliged to respond to these objections. Generally, if it is necessary to revoke changes, we will notify under the PR and provide the reasons.

A Contributor is any individual who contributes to the project, ranging from minor typo fixes to significant bug resolutions.

Individuals seeking core contributor status are required to have made substantive contributions to the minibp project (noting that such contributions are not easily quantifiable). An issue should then be opened outlining the justification for this role. We will endeavor to respond in a timely manner.

If you are a security researcher and have found a vulnerability in minibp(Although this is hard to encounter, in any case), please DO NOT open a public issue. Use the GitHub Security Advisory feature to send it.

## 4.About the Versioning

Releases of minibp are irregular, and no beta versions are provided.

Versioning adheres strictly to the 0.x scheme. The value of x is non-indicative and carries no semantic weight. Any given version may constitute either a minor revision or a substantial, potentially breaking change.

A change in the latest version number from 0.x to 1.0 shall signify the conclusion of all development and maintenance efforts for minibp. We appreciate your understanding regarding the inherent limitations of our time and capacity.

Of course, there is an exception, which is if we decide to continue development, then the next version release will change the minor version number, and the version that stops being updated will be released as a major version, and so on..

## 5. About the style

You **MUST RUN** `go fmt` before you commit your code.

Please follow standard Go naming conventions: use `MixedCaps` (PascalCase) for exported identifiers and `camelCase` for unexported variables.

Please use human-friendly function, variable, type names. Meaningless or ambiguous words **ARE NOT** allowed in any identifiers.

Comments in code **MUST BE** English, not other languages.


## 6. About the testing

You **MUST** test your changes before contributing.

Please run the following commands in order for testing:

- 1. go build -o minibp cmd/minibp/main.go
- 2 ../minibp -a
- 3. ninja -v
- 4. cd examples && ../minibp -a && ninja -v (go test does not necessarily represent real-world execution scenarios. As such, this step is required)

Please ensure that Java, GCC, G++, and Ninja-build are installed on the system.

#### Why not go run ?

Because if you use 'go run' to generate build.ninja, the minibp used to rebuild this target points to a temporary path like /tmp/go-build, which is unreliable.
