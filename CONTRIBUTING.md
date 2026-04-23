## Before contributing

This guideline explains how to contribute to minibp. You **MUST** read this before writing or refactoring any code.

When contributing to this project (via `git push` or Pull Request), these guidelines apply, and you are held responsible for the code you commit.

Last updated date: Please refer to the commit history. 

Please note that **we do not track changes to CONTRIBUTING.md, so kindly keep this in mind**.

## 1. About the AI code

We accept the use of AI-assisted or AI-generated code. While AI is fast and capable, we still expect that:  

- 1. You should understand at least half or all of the logic, and be aware of how AI-written code may impact the system.  

- 2. Follow our coding standards.

- 3. For PRs initiated by an AI Agent, we **must know** who **instructed** the AI Agent to initiate it.

## 2. About the commit

Commit messages should be concise and clear, with the first letter capitalized, and the title should not exceed 50 characters.

If a body is needed, be sure to explain the "what," "why," and "how." The body should not exceed 200 characters.


Please note, do not use [Conventional Commits](https://www.conventionalcommits.org/) as the standard for commit messages.

## 3. About the issues, pull requests and contributors

Issues and features should be documented and archived in the form of an issue.

Before initiating a major change, please open an issue first, and then link to that issue in the pull request.

The submission of any meaningless or extraneous information within issues and pull requests is absolutely forbidden. Offending comments, issues, or pull requests may be closed and locked, deleted, or reported as appropriate.

A Contributor is any individual who contributes to the project, ranging from minor typo fixes to significant bug resolutions.

Individuals seeking core contributor status are required to have made substantive contributions to the minibp project (noting that such contributions are not easily quantifiable). An issue should then be opened outlining the justification for this role. We will endeavor to respond in a timely manner.

If you are a security researcher and have found a vulnerability in minibp(Although this is hard to encounter, in any case), please DO NOT open a public issue. Use the GitHub Security Advisory feature to send it.

## 4.About the Versioning

Releases of minibp are irregular, and no beta versions are provided.

Versioning adheres strictly to the 0.x scheme. The value of x is non-indicative and carries no semantic weight. Any given version may constitute either a minor revision or a substantial, potentially breaking change.

A change in the latest version number from 0.x to 1.0 shall signify the conclusion of all development and maintenance efforts for minibp. We appreciate your understanding regarding the inherent limitations of our time and capacity.

Of course, there is an exception, which is if we decide to continue development, then the next time updates stop will be 1.x, and so on.

## 5. About the style

You should run `go fmt` before you commit your code.

Please follow standard Go naming conventions: use `MixedCaps` (PascalCase) for exported identifiers and `camelCase` for unexported variables.

Please use human-friendly function, variable, type names. Meaningless or ambiguous words **ARE NOT** allowed in any identifiers.

Comments in code **MUST BE** English, not other languages.


## 6. About the testing

You **MUST** test your changes before contributing.

Please run the following commands in order for testing:

- 1. go build -o minibp cmd/minibp/main.go
- 2 ../minibp -a
- 3. ninja
- 4. cd examples && ../minibp -a && ninja(go test does not necessarily represent real-world execution scenarios. As such, this step is required)

Please ensure that Java, GCC, G++, and Ninja-build are installed on the system.

#### Why not go run ?

Because if you use 'go run' to generate build.ninja, the minibp used to rebuild this target points to a temporary path like /tmp/go-build, which is unreliable.
