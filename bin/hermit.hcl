manage-git = false
sources = ["env:///bin/hermit-packages", "https://github.com/cashapp/hermit-packages.git"]
env = {
  GOBIN: "${HERMIT_ENV}/out/bin",
  PATH: "${GOBIN}:${PATH}"
}
