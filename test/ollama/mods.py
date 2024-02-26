import openai  # openai v1.0.0+
import threading


def send_request():
    client = openai.OpenAI(
        api_key="anything", base_url="http://0.0.0.0:8000"
    )  # set proxy to base_url
    # request sent to model set on litellm proxy, `litellm --model`
    response = client.chat.completions.create(
        model="ollama",
        messages=[
            {"role": "user", "content": "this is a test request, write a short poem"}
        ],
    )
    print(response)


for i in range(10):
    threading.Thread(target=send_request, args=()).start()
