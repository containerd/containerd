# Containerd website

The containerd website at https://containerd.io is built using [Hugo](https://gohugo.io) and published to [Netlify](https://netlify.com).

To develop the site locally in "watch" mode (using Docker):

```bash
$ docker run -it -v $(pwd):/src -p "1313:1313" -e HUGO_WATCH=true jojomi/hugo
```

You can then open up your browser to localhost:1313 to see the rendered site. The site auto-refreshes when you modify files locally.

