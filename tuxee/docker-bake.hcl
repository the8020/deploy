target "platform" {
  context = "."
  dockerfile = "Dockerfile"
  target = "builder"
  args = { VERSION = "0.10" }
}

target "tuxee" {
  context = "tuxee"
  dockerfile = "Dockerfile"
  contexts = { platform = "target:platform" }
  tags = ["the8020:tuxee-preview"]
}
