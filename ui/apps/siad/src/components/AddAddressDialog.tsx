import {
  DialogTitle,
  Flex,
  Paragraph,
  TextField,
  Button,
  Label,
  Text,
} from '@siafoundation/design-system'
import { useWalletAddressCreate } from '@siafoundation/react-siad'
import { useFormik } from 'formik'
import { useDialog } from '../contexts/dialog'
import * as Yup from 'yup'

const exampleAddr =
  'addr:e3b1050aef388438668b52983cf78f40925af8f0aa8b9de80c18eadcefce8388d168a313e3f2'

const initialValues = {
  address: '',
  description: '',
  index: 0,
}

const validationSchema = Yup.object().shape({
  address: Yup.string().required('Required'),
  description: Yup.string().required('Required'),
  index: Yup.number().integer().required('Required'),
})

export function AddAddressDialog() {
  const { closeDialog } = useDialog()
  const addAddress = useWalletAddressCreate()

  const formik = useFormik({
    initialValues,
    validationSchema,
    onSubmit: async (values, actions) => {
      const response = await addAddress.post({
        param: values.address,
        payload: {
          index: values.index || 0,
          description: values.description || '',
        },
      })
      if (response.status !== 200) {
        actions.setStatus(response)
      } else {
        actions.resetForm()
        closeDialog()
      }
    },
  })

  return (
    <Flex direction="column" gap="2">
      <DialogTitle>Add Address</DialogTitle>
      <Paragraph size="14">Add an address to cold storage wallet.</Paragraph>
      <form onSubmit={formik.handleSubmit}>
        <Flex direction="column" gap="2">
          <Field
            formik={formik}
            title="Address"
            name="address"
            placeholder={exampleAddr}
            autoComplete="off"
            type="text"
          />
          <Field
            formik={formik}
            title="Description"
            name="description"
            placeholder="My address"
            autoComplete="off"
            type="text"
          />
          <Field
            formik={formik}
            title="Index"
            name="index"
            placeholder="0"
            type="number"
          />
          {formik.status?.error && (
            <Text css={{ color: '$red11' }}>{formik.status.error}</Text>
          )}
          <Button size="2" variant="accent" type="submit">
            Add
          </Button>
        </Flex>
      </form>
    </Flex>
  )
}

type FieldProps = {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  formik: any
  title: string
  name: string
  placeholder: string
  autoComplete?: string
  type?: string
}

function Field({
  formik,
  title,
  name,
  placeholder,
  autoComplete,
  type,
}: FieldProps) {
  return (
    <Flex direction="column" gap="1">
      <Flex justify="between">
        <Label htmlFor={name} css={{ color: '$gray9' }}>
          {title}
        </Label>
        {formik.errors[name] && formik.touched[name] && (
          <Text css={{ color: '$red11' }}>{formik.errors[name]}</Text>
        )}
      </Flex>
      <TextField
        id={name}
        name={name}
        autoComplete={autoComplete}
        placeholder={placeholder}
        type={type}
        onChange={formik.handleChange}
        value={formik.values[name]}
        size="2"
      />
    </Flex>
  )
}
