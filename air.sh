# ${IMAGE//\//\\/} is needed to escape all of the forward slashes in IMAGE
sed "s/%IMAGE%/${IMAGE//\//\\/}/g" .air-template.toml > .air.toml
exec air -c .air.toml
