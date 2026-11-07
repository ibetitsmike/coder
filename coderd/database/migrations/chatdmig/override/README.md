An upstream migration that creates objects in both schemas is "mixed".
`chatdmig route` stops and asks for `override/<name>.up.sql`: the same change,
written so that chat objects land in chatd and core objects land in public.
It runs as the node of that version, with `search_path` chatd, public, so
qualify core objects that it creates (`CREATE TABLE public.foo`).
