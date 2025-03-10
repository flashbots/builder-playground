Reset the state
rm state.json
vim {"version": 1} > state.json

op-deployer apply --workdir . --deployment-target genesis

op-deployer inspect genesis --workdir . --outfile ./genesis.json 13

op-deployer inspect rollup --workdir . --outfile ./rollup.json 13
