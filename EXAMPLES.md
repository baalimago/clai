# Examples

All of the commands support xargs-like `-i`/`-I`/`-replace` flags.

Example: `clai h | clai -i q Summarize this for me: {}`, this would summarize the output of `clai h`.

Regardless of you wish to generate a photo, continue a chat or reply to your previous query, the prompt system remains the same.

```bash
clai help `# For more info about the available commands (and shorthands)`
```

### Queries
```bash
clai query My favorite color is blue, tell me some facts about it
```
```bash
clai -re `# Use the -re flag to use the previous query as context for some next query` \
    q Write a poem about my favorite colour 
```

Personally I have `alias ask=clai q` and then `alias rask=clai -re q`.
This way I can `ask` -> `rask` -> `rask` for a temporary conversation.

Every 'temporary conversation' is also saved as a chat, so it's possible to continue it later, see below on how to list chats.

### Tooling
Many vendors support function calling/tooling.
This basically means that the AI model will ask *your local machine* to run some command, then it will analyze the output of said command.

See all the currently available tools [here](./internal/tools/), please create an issue if you'd like to see some tool added.
```bash
clai -t q  `# Specify you wish to enable tools with -t/-tools` \
   Analyze the project found at ~/Projects/clai and give me a brief summary of what it does
```

ChatGPT has native support and works well.
As of 2024-05, claude does not have support for tools + streaming, but works otherwise.
Mistral tooling works, but it's so overly [Pydantic](https://docs.pydantic.dev/latest/) that it breaks the generic solution, so I've chosen to not have it enable it for now.

### Chatting
```bash
clai -chat-model claude-3-opus-20240229 `  # Using some other model` \
    chat new Lets have a conversation about Hegel
```

The `-cm`/`-chat-model` flag works for any text-like command.
Meaning: you can start a conversation with one chat model, then continue it with another.
```bash
clai chat list
```
```bash
clai c continue Lets_have_a_conversation_about
```

```bash
clai c continue 1 kant is better `# Continue some previous chat with message ` 
```

### Globs
```bash
clai -raw `# Don't format output as markdown` \
    glob '*.go' Generate a README for this project > README.md
```
The `-raw` flag will ensure that the output stays what the model outputs, without `glow` or animations.

### Photos
```bash
printf "flowers" | clai -i `    # stdin replacement works for photos also` \
    --photo-prefix=flowercat `  # Sets the prefix for local photo` \
    --photo-dir=/tmp `          # Sets the output dir` \
    photo A cat made out of {}
```

Since -N alternatives are disabled for many newer OpenAI models, you can use [repeater](https://github.com/baalimago/repeater) to generate several responses from the same prompt:
```bash
NO_COLOR=true repeater -n 10 -w 3 -increment -file out.txt -output BOTH \
    clai -pp flower_INC p A cat made of flowers
```


## Configuration
`clai` will create configuration files at [os.GetConfigDir()](https://pkg.go.dev/os#UserConfigDir)`/.clai/`.
First time you run `clai`, two default command-related ones, `textConfig.json` and `photoConfig.json`,  will be created and then one for each specific model.
The configuration presedence is as follows (from lowest to highest):
1. Default hard-coded configurations [such as this](./internal/text/conf.go), these gets written to file first time you run `clai`
1. Configurations from local `textConfig.json` or `photoConfig.json` file
1. Flags

The `textConfig.json/photoConfig.json` files configures _what_ you want done, not _how_ the models should perform it.
This way it scales for any vendor + model.

### Models
There's two ways to configure the models:
1. Set flag `-chat-model` or `-photo-model` 
1. Set the `model` field in the `textConfig.json` or `photoConfig.json` file. This will make it default, if not overwritten by flags.

Then, for each model, a new configuration file will be created.
Since each vendor's model supports quite different configurations, the model configurations aren't exposed as flags.
Instead, modify the model by adjusting its configuration file, found in [os.GetConfigDir()](https://pkg.go.dev/os#UserConfigDir)`/.clai/<vendor>_<model-type>_<model-name>.json`.
This config json will in effect be unmarshaled into a request send to the model's vendor.

### Conversations
Within [os.GetConfigDir()](https://pkg.go.dev/os#UserConfigDir)`/.clai/conversations` you'll find all the conversations.
You can also modify the chats here as a way to prompt, or create entirely new ones as you see fit.
