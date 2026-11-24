def scalar(v):
  if type(v) == "bool":
    return "true" if v else "false"
  if type(v) == "int" or type(v) == "float":
    return str(v)
  return "\"" + str(v).replace("\\", "\\\\").replace("\"", "\\\"") + "\""

def emit(v, indent):
  lines = []
  for k in sorted(v.keys()):
    x = v[k]
    if type(x) == "dict":
      lines.append(indent + k + ":")
      lines += emit(x, indent + "  ")
    elif type(x) == "list":
      lines.append(indent + k + ":")
      for e in x:
        lines.append(indent + "  - " + scalar(e))
    else:
      lines.append(indent + k + ": " + scalar(x))
  return lines

spec = None
for r in ctx.resource_list["items"]:
  if r["kind"] == "PorchConfig":
    spec = r["spec"]
for r in ctx.resource_list["items"]:
  if r["kind"] == "ConfigMap" and r["metadata"]["name"] == "porch-config":
    r["data"]["porch.yaml"] = "\n".join(emit(spec, "")) + "\n"
