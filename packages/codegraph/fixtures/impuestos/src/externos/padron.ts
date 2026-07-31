import axios from "axios";

export async function consultarPadron(rut: string) {
  const { data } = await axios.get(`https://padron.example/${rut}`);
  return data as { exento: boolean };
}
