# twitter-text 적합성 자료

- `validate.yml`, `extract.yml`, `LICENSE`: [twitter/twitter-text](https://github.com/twitter/twitter-text/tree/master/conformance)의 공식 적합성 사례 원본 (Apache License 2.0). 2026-10-27에 master에서 받았다.
- `conformance.json`: 위 YAML에서 이 앱이 쓰는 부분(가중 길이 v2·v3, 방향 표시 문자, URL 추출)만 JSON으로 옮긴 것. 내용은 바꾸지 않았다. YAML 파서를 운영 의존성으로 두지 않으려고 JSON으로 둔다.

갱신할 때: YAML을 새로 받고 같은 키로 JSON을 다시 만든 뒤 `make test`를 돌린다 (Go `internal/thread` 적합성 테스트와 `web/src/lib/thread.test.ts`가 이 파일을 읽는다).

`../thread/golden.json`은 공식 JavaScript 라이브러리(`web/node_modules/twitter-text`)로 계산한 기대값이다. `cd web && npm run gen:thread`로 다시 만든다.
