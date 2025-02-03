SRC=*.ts

dist.js: $(SRC) rollup.main.js tsconfig.json
	tsc
	node_modules/.bin/rollup -c rollup.main.js
